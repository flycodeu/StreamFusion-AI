package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import com.streamfusion.platform.system.access.query.AccessQueryService;
import com.streamfusion.platform.system.auth.service.BootstrapService;
import com.streamfusion.platform.system.auth.service.PasswordService;
import java.time.LocalDateTime;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

/**
 * The maintained seed and real MVC authorization are exercised on isolated H2, not production
 * MySQL.
 */
@SpringBootTest(
        properties = "spring.datasource.url=jdbc:h2:mem:access_query;MODE=MySQL;DB_CLOSE_DELAY=-1")
@AutoConfigureMockMvc
@ActiveProfiles("test")
class AccessQueryIntegrationTest extends SecureLoginSupport {
    private static final long SUBJECT = 5010L;
    private static final long FIRST_ROLE = 6010L;
    private static final long SECOND_ROLE = 6011L;
    private static final String ROOT = "/access-query";
    private static final List<String> ENDPOINTS =
            List.of(
                    ROOT + "/users/page",
                    ROOT + "/pages/page",
                    ROOT + "/roles/page",
                    ROOT + "/roles/6010/pages",
                    ROOT + "/roles/6010/users",
                    ROOT + "/pages/overview",
                    ROOT + "/pages/export.xlsx",
                    ROOT + "/users/5010/pages",
                    ROOT + "/pages/1002/users",
                    ROOT + "/pages/1002/roles");

    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired PasswordService passwords;
    private JdbcTemplate jdbc;
    private MockHttpSession admin;
    private String hash;

    @BeforeEach
    void initialize() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        admin = login("Admin01", "AdminPass1!");
        hash = passwords.encode("Subject1!");
        addUser(SUBJECT, "Subject01");
        addRole(FIRST_ROLE, "FIRST", true);
        addRole(SECOND_ROLE, "SECOND", true);
        assign(SUBJECT, FIRST_ROLE, 1002);
        assign(SUBJECT, SECOND_ROLE, 1002);
    }

    @Test
    void everyEndpointRejectsAnonymousAndUsersWithoutThisPage() throws Exception {
        MockHttpSession ordinary = login("Subject01", "Subject1!");
        for (String path : ENDPOINTS) {
            mvc.perform(get(path)).andExpect(status().isUnauthorized());
            mvc.perform(get(path).session(ordinary)).andExpect(status().isForbidden());
        }
    }

    @Test
    void ordinaryPageDelegationWorksWithoutManagementModulesAndNextRequestRechecksRevocation()
            throws Exception {
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=1 AND menu_id=1012",
                                Integer.class))
                .isOne();
        addUser(5011, "Reader01");
        addRole(6020, "READER", true);
        assign(5011, 6020, 1012);
        MockHttpSession reader = login("Reader01", "Subject1!");
        for (String path : ENDPOINTS) {
            mvc.perform(get(path).session(reader)).andExpect(status().isOk());
        }
        mvc.perform(get("/user/page").session(reader)).andExpect(status().isForbidden());
        mvc.perform(get("/roles").session(reader)).andExpect(status().isForbidden());
        mvc.perform(get("/menus").session(reader)).andExpect(status().isForbidden());
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=6020 AND menu_id=1012");
        for (String path : ENDPOINTS) {
            mvc.perform(get(path).session(reader)).andExpect(status().isForbidden());
        }
    }

    @Test
    void multipleRoleSourcesDeduplicateAndDisabledSourcesRemainExplainable() throws Exception {
        JsonNode first = userPage(false);
        assertThat(first.path("total").asInt()).isOne();
        assertThat(first.at("/items/0/sourceCount").asInt()).isEqualTo(2);
        assertThat(first.at("/items/0/sourcePreview").size()).isEqualTo(2);
        jdbc.update("UPDATE sys_role SET status='DISABLED' WHERE id=?", SECOND_ROLE);
        assertThat(userPage(false).at("/items/0/accessEligible").asBoolean()).isTrue();
        JsonNode roles = read(ROOT + "/pages/1002/roles?userId=5010&includeInactive=true");
        assertThat(roles.path("total").asInt()).isEqualTo(2);
        assertThat(roles.at("/items/1/role/status").asText()).isEqualTo("DISABLED");
        assertThat(roles.at("/items/1/grantEffective").asBoolean()).isFalse();
        jdbc.update("UPDATE sys_role SET status='DISABLED' WHERE id=?", FIRST_ROLE);
        assertThat(userPage(false).path("total").asInt()).isZero();
        JsonNode inactive = userPage(true);
        assertThat(inactive.at("/items/0/grantEffective").asBoolean()).isFalse();
        assertThat(codes(inactive.at("/items/0/blockers"))).contains("NO_ACTIVE_ROLE");
        JsonNode reverse = read(ROOT + "/pages/1002/users?keyword=Subject&includeInactive=true");
        assertThat(reverse.path("total").asInt()).isOne();
        assertThat(reverse.at("/items/0/sourceCount").asInt()).isEqualTo(2);
    }

    @Test
    void overviewsSeparateConfiguredRelationsFromCurrentEligibility() throws Exception {
        JsonNode roles = read(ROOT + "/roles/page?keyword=FIRST");
        assertThat(roles.path("total").asInt()).isOne();
        assertThat(roles.at("/items/0/assignedUserCount").asInt()).isOne();
        assertThat(roles.at("/items/0/usableUserCount").asInt()).isOne();
        assertThat(roles.at("/items/0/assignedPageCount").asInt()).isOne();
        assertThat(roles.at("/items/0/effectivePageCount").asInt()).isOne();
        JsonNode rolePages = read(ROOT + "/roles/6010/pages");
        assertThat(rolePages.at("/items/0/menu/id").asText()).isEqualTo("1002");
        assertThat(read(ROOT + "/roles/6010/users").at("/items/0/username").asText())
                .isEqualTo("Subject01");
        JsonNode pages = read(ROOT + "/pages/overview?keyword=用户管理");
        assertThat(pages.path("total").asInt()).isOne();
        // The seed also grants every PAGE to SUPER_ADMIN and bootstrap links it to admin.
        assertThat(pages.at("/items/0/assignedRoleCount").asInt()).isEqualTo(3);
        assertThat(pages.at("/items/0/assignedUserCount").asInt()).isEqualTo(2);
        assertThat(pages.at("/items/0/eligibleUserCount").asInt()).isEqualTo(2);
        jdbc.update("UPDATE sys_role SET status='DISABLED' WHERE id=?", FIRST_ROLE);
        jdbc.update("UPDATE sys_role SET status='DISABLED' WHERE id=?", SECOND_ROLE);
        assertThat(read(ROOT + "/roles/page?keyword=FIRST").at("/items/0/usableUserCount").asInt())
                .isZero();
        JsonNode after = read(ROOT + "/pages/overview?keyword=用户管理");
        assertThat(after.at("/items/0/assignedUserCount").asInt()).isEqualTo(2);
        assertThat(after.at("/items/0/activeRoleCount").asInt()).isOne();
        assertThat(after.at("/items/0/eligibleUserCount").asInt()).isOne();
    }

    @Test
    void accountDetailsIncludeAllDepartmentsWithoutDuplicatingPageAccounts() throws Exception {
        jdbc.update("INSERT INTO sys_user_dept(user_id,dept_id) VALUES(?,?)", SUBJECT, 2002);
        jdbc.update("INSERT INTO sys_user_dept(user_id,dept_id) VALUES(?,?)", SUBJECT, 2003);
        JsonNode page = read(ROOT + "/pages/1002/users?keyword=Subject&includeInactive=true");
        assertThat(page.path("total").asInt()).isOne();
        assertThat(page.at("/items/0/user/departments").size()).isEqualTo(2);
        assertThat(page.at("/items/0/user/departments/0/name").asText()).isEqualTo("研发部门");
        assertThat(page.at("/items/0/user/departments/1/name").asText()).isEqualTo("运维部门");
        assertThat(read(ROOT + "/roles/6010/users").at("/items/0/departments").size()).isEqualTo(2);
        assertThat(read(ROOT + "/users/page?keyword=Subject").at("/items/0/departments").size())
                .isEqualTo(2);
    }

    @Test
    void pageExportIsRealXlsxWithOneRowPerPageAndEligibleUserCount() throws Exception {
        jdbc.update("INSERT INTO sys_user_dept(user_id,dept_id) VALUES(?,?)", SUBJECT, 2002);
        jdbc.update("INSERT INTO sys_user_dept(user_id,dept_id) VALUES(?,?)", SUBJECT, 2003);
        var result =
                mvc.perform(
                                get(ROOT + "/pages/export.xlsx")
                                        .param("keyword", "用户管理")
                                        .session(admin))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getResponse();
        assertThat(result.getContentType())
                .isEqualTo("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet");
        assertThat(result.getHeader("Content-Disposition"))
                .matches("attachment; filename=\"access-pages-[0-9]{8}-[0-9]{6}\\.xlsx\"");
        assertThat(result.getContentAsByteArray()).startsWith((byte) 'P', (byte) 'K');
        String worksheet =
                workbookEntry(result.getContentAsByteArray(), "xl/worksheets/sheet1.xml");
        assertThat(worksheet).contains("页面", "当前具备资格账号数", "账号", "部门");
        assertThat(worksheet).contains("<c r=\"B2\"><v>2</v></c>");
        assertThat(worksheet).contains("Subject01");
        assertThat(worksheet).contains("研发部门、运维部门");
        assertThat(worksheet).doesNotContain("<f>", "<f ");
        assertThat(workbookEntry(result.getContentAsByteArray(), "xl/workbook.xml"))
                .contains("页面汇总");
    }

    @Test
    void pageExportMatchesEligibleCountAndKeepsDisabledPageWithZeroAccounts() throws Exception {
        jdbc.update(
                "UPDATE sys_role SET status='DISABLED' WHERE id IN (?,?)", FIRST_ROLE, SECOND_ROLE);
        assertThat(
                        read(ROOT + "/pages/overview?keyword=用户管理")
                                .at("/items/0/eligibleUserCount")
                                .asInt())
                .isOne();
        var onlyAdmin =
                mvc.perform(
                                get(ROOT + "/pages/export.xlsx")
                                        .param("keyword", "用户管理")
                                        .session(admin))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getResponse();
        String activeSheet =
                workbookEntry(onlyAdmin.getContentAsByteArray(), "xl/worksheets/sheet1.xml");
        assertThat(activeSheet).contains("<c r=\"B2\"><v>1</v></c>", "Admin01");
        assertThat(activeSheet).doesNotContain("Subject01");

        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1002");
        assertThat(
                        read(ROOT + "/pages/overview?keyword=用户管理")
                                .at("/items/0/eligibleUserCount")
                                .asInt())
                .isZero();
        var disabled =
                mvc.perform(
                                get(ROOT + "/pages/export.xlsx")
                                        .param("keyword", "用户管理")
                                        .session(admin))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getResponse();
        String disabledSheet =
                workbookEntry(disabled.getContentAsByteArray(), "xl/worksheets/sheet1.xml");
        assertThat(disabledSheet).contains("用户管理", "<c r=\"B2\"><v>0</v></c>");
        assertThat(disabledSheet).doesNotContain("Admin01", "Subject01");
    }

    @Test
    void failedExportReturnsJsonErrorWithoutAttachmentHeaders() throws Exception {
        var response =
                mvc.perform(
                                get(ROOT + "/pages/export.xlsx")
                                        .param("keyword", "x".repeat(65))
                                        .session(admin))
                        .andExpect(status().isBadRequest())
                        .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"))
                        .andReturn()
                        .getResponse();
        assertThat(response.getHeader("Content-Disposition")).isNull();
        assertThat(response.getContentType()).contains("application/json");
    }

    private static String workbookEntry(byte[] archive, String expected) throws Exception {
        try (var zip =
                new java.util.zip.ZipInputStream(new java.io.ByteArrayInputStream(archive))) {
            java.util.zip.ZipEntry entry;
            while ((entry = zip.getNextEntry()) != null) {
                if (expected.equals(entry.getName()))
                    return new String(zip.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
            }
        }
        throw new AssertionError("Missing XLSX part " + expected);
    }

    @Test
    void previewsStayBoundedAndFullSourcesArePaginatedWithOneSnapshotExplanation()
            throws Exception {
        for (int index = 2; index < 7; index++) {
            addRole(6010 + index, "EXTRA" + index, true);
            assign(SUBJECT, 6010 + index, 1002);
        }
        JsonNode result = userPage(true);
        assertThat(result.at("/items/0/sourceCount").asInt()).isEqualTo(7);
        assertThat(result.at("/items/0/sourcePreview").size()).isEqualTo(3);
        JsonNode reverse = read(ROOT + "/pages/1002/users?keyword=Subject&includeInactive=true");
        assertThat(reverse.at("/items/0/sourcePreview").size()).isEqualTo(3);
        JsonNode sources =
                read(ROOT + "/pages/1002/roles?userId=5010&includeInactive=true&page=2&size=2");
        assertThat(sources.path("total").asInt()).isEqualTo(7);
        assertThat(sources.path("items").size()).isEqualTo(2);
        assertThat(sources.at("/subjectAccess/sourceCount").asInt()).isEqualTo(7);
        assertThat(sources.at("/subjectAccess/accessEligible").asBoolean()).isTrue();
        assertThat(sources.path("checkedAt").asText()).isNotBlank();
    }

    @Test
    void hiddenPageAndAncestorDoNotRevokeButDisabledPageOrAncestorDoes() throws Exception {
        grant(FIRST_ROLE, 1101);
        jdbc.update("UPDATE sys_menu SET visible=FALSE WHERE id IN (1100,1101)");
        JsonNode hidden = read(ROOT + "/pages/1101/users?userId=5010&includeInactive=true");
        assertThat(hidden.at("/subjectPage/navigationVisible").asBoolean()).isFalse();
        assertThat(hidden.at("/items/0/accessEligible").asBoolean()).isTrue();
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1100");
        assertThat(read(ROOT + "/pages/1101/users?userId=5010").path("total").asInt()).isZero();
        JsonNode ancestor = read(ROOT + "/pages/1101/users?userId=5010&includeInactive=true");
        assertThat(codes(ancestor.at("/items/0/blockers"))).contains("ANCESTOR_DISABLED");
        assertThat(ancestor.at("/items/0/grantEffective").asBoolean()).isFalse();
        jdbc.update("UPDATE sys_menu SET enabled=TRUE WHERE id=1100");
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1101");
        JsonNode page = read(ROOT + "/pages/1101/roles?userId=5010&includeInactive=true");
        assertThat(codes(page.at("/subjectAccess/blockers"))).contains("PAGE_DISABLED");
        assertThat(page.at("/items/0/grantEffective").asBoolean()).isFalse();
    }

    @Test
    void missingPageGrantCanCoexistWithModuleAccessWithoutAddingUnassignedUsersToDefaultLists()
            throws Exception {
        grant(FIRST_ROLE, 1101);
        assertThat(read(ROOT + "/pages/1103/users?userId=5010").path("total").asInt()).isZero();
        assertThat(
                        read(ROOT + "/pages/1103/users?keyword=Subject&includeInactive=true")
                                .path("total")
                                .asInt())
                .isZero();
        JsonNode specific = read(ROOT + "/pages/1103/users?userId=5010&includeInactive=true");
        assertThat(specific.path("total").asInt()).isOne();
        assertThat(specific.at("/items/0/sourceCount").asInt()).isZero();
        assertThat(specific.at("/items/0/grantEffective").asBoolean()).isFalse();
        assertThat(specific.at("/items/0/accessEligible").asBoolean()).isFalse();
        assertThat(specific.at("/items/0/moduleAccess").asBoolean()).isTrue();
        assertThat(codes(specific.at("/items/0/blockers"))).contains("NO_PAGE_GRANT");
        JsonNode detail = read(ROOT + "/pages/1103/roles?userId=5010&includeInactive=true");
        assertThat(detail.path("total").asInt()).isZero();
        assertThat(detail.at("/subjectAccess/moduleAccess").asBoolean()).isTrue();
        assertThat(detail.at("/subjectAccess/accessEligible").asBoolean()).isFalse();
    }

    @Test
    void superAdminAlsoNeedsAnActualPageRelationship() throws Exception {
        long adminId =
                jdbc.queryForObject("SELECT id FROM sys_user WHERE username='Admin01'", Long.class);
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=1 AND menu_id=1101");
        JsonNode result =
                read(ROOT + "/pages/1101/users?userId=" + adminId + "&includeInactive=true");
        assertThat(result.at("/items/0/sourceCount").asInt()).isZero();
        assertThat(result.at("/items/0/accessEligible").asBoolean()).isFalse();
        assertThat(result.at("/items/0/moduleAccess").asBoolean()).isTrue();
        assertThat(codes(result.at("/items/0/blockers"))).contains("NO_PAGE_GRANT");
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=1 AND menu_id=1012");
        mvc.perform(get(ROOT + "/users/page").session(admin)).andExpect(status().isForbidden());
    }

    @ParameterizedTest
    @CsvSource({
        "0,false,PASSWORD_CHANGE_REQUIRED",
        "1,true,PASSWORD_CHANGE_REQUIRED",
        "2,false,USER_DISABLED"
    })
    void accountRestrictionsAreSeparateFromPageGrant(int state, boolean mustChange, String code)
            throws Exception {
        jdbc.update(
                "UPDATE sys_user SET status=?,must_change_password=? WHERE id=?",
                state,
                mustChange,
                SUBJECT);
        assertThat(userPage(false).path("total").asInt()).isZero();
        JsonNode row = userPage(true).at("/items/0");
        assertThat(row.path("grantEffective").asBoolean()).isTrue();
        assertThat(row.path("accountUsable").asBoolean()).isFalse();
        assertThat(row.path("accessEligible").asBoolean()).isFalse();
        assertThat(codes(row.path("blockers"))).contains(code);
        assertThat(read(ROOT + "/pages/1002/users?userId=5010").path("total").asInt()).isZero();
    }

    @Test
    void temporaryLoginLockDoesNotBecomeARevocation() throws Exception {
        jdbc.update(
                "UPDATE sys_user SET locked_until=? WHERE id=?",
                LocalDateTime.now().plusYears(1),
                SUBJECT);
        JsonNode result = userPage(false);
        assertThat(result.at("/subjectUser/loginRestricted").asBoolean()).isTrue();
        assertThat(result.at("/subjectUser/accountUsable").asBoolean()).isTrue();
        assertThat(result.at("/items/0/accessEligible").asBoolean()).isTrue();
        assertThat(result.at("/items/0/blockers").size()).isZero();
    }

    @Test
    void invalidAncestorCycleFailsClosedForPageAndModuleQualification() throws Exception {
        grant(FIRST_ROLE, 1101);
        jdbc.update("UPDATE sys_menu SET parent_id=1100 WHERE id=1100");
        JsonNode result = read(ROOT + "/pages/1101/roles?userId=5010&includeInactive=true");
        assertThat(result.at("/subjectPage/chainEnabled").asBoolean()).isFalse();
        assertThat(result.at("/subjectAccess/moduleAccess").asBoolean()).isFalse();
        assertThat(result.at("/subjectAccess/accessEligible").asBoolean()).isFalse();
        assertThat(codes(result.at("/subjectAccess/blockers"))).contains("INVALID_MENU_TREE");
    }

    @Test
    void selectionCatalogsAndRoleSourceSearchStayPaginatedAndMinimal() throws Exception {
        JsonNode pages = read(ROOT + "/pages/page?size=2");
        assertThat(pages.path("total").asInt()).isEqualTo(15);
        assertThat(pages.path("items").size()).isEqualTo(2);
        JsonNode users = read(ROOT + "/users/page?size=1&page=2");
        assertThat(users.path("total").asInt()).isEqualTo(2);
        assertThat(users.path("items").size()).isOne();
        JsonNode roles = read(ROOT + "/pages/1002/roles?userId=5010&keyword=FIRST&size=1");
        assertThat(roles.path("total").asInt()).isOne();
        assertThat(roles.at("/items/0/role/code").asText()).isEqualTo("FIRST");
        assertThat(roles.at("/items/0/userCount").asInt()).isOne();
    }

    @Test
    void unregisteredModuleCannotBeReportedAsUsable() throws Exception {
        jdbc.update("UPDATE sys_menu SET module_key='not-registered' WHERE id=1002");
        assertThat(read(ROOT + "/users/5010/pages").path("total").asInt()).isZero();
        JsonNode row = read(ROOT + "/users/5010/pages?includeInactive=true").at("/items/0");
        assertThat(row.path("moduleAccess").asBoolean()).isFalse();
        assertThat(row.path("grantEffective").asBoolean()).isFalse();
        assertThat(codes(row.path("blockers"))).contains("MODULE_UNREGISTERED");
    }

    @Test
    void reversePaginationCountsUsersRatherThanRoleJoinsAndEscapesWildcards() throws Exception {
        for (int index = 2; index <= 6; index++) {
            addUser(5010 + index, "Subject0" + index);
            assign(5010 + index, FIRST_ROLE, 1002);
            assign(5010 + index, SECOND_ROLE, 1002);
        }
        JsonNode page = read(ROOT + "/pages/1002/users?keyword=Subject&page=2&size=2");
        assertThat(page.path("total").asInt()).isEqualTo(6);
        assertThat(page.path("items").size()).isEqualTo(2);
        assertThat(
                        read(ROOT + "/pages/1002/users?keyword=Subject&page=4&size=2")
                                .path("items")
                                .size())
                .isZero();
        jdbc.update("UPDATE sys_user SET nickname='literal_%!' WHERE id=?", SUBJECT);
        for (String keyword : List.of("_", "%", "!")) {
            JsonNode found =
                    data(
                            mvc.perform(
                                            get(ROOT + "/users/page")
                                                    .session(admin)
                                                    .param("keyword", keyword))
                                    .andExpect(status().isOk())
                                    .andReturn()
                                    .getResponse()
                                    .getContentAsByteArray());
            assertThat(found.path("total").asInt()).isOne();
            assertThat(found.at("/items/0/id").asText()).isEqualTo("5010");
        }
    }

    @Test
    void invalidRequestsAndMissingObjectsUseTheExistingErrorContract() throws Exception {
        for (String query :
                List.of("page=0", "page=-1", "size=0", "size=101", "moduleKey=bad%20module")) {
            mvc.perform(get(ROOT + "/users/page?" + query).session(admin))
                    .andExpect(status().isBadRequest())
                    .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        }
        mvc.perform(get(ROOT + "/users/page").session(admin).param("keyword", "x".repeat(65)))
                .andExpect(status().isBadRequest());
        for (String path :
                List.of(
                        "/users/0/pages",
                        "/users/abc/pages",
                        "/pages/0/users",
                        "/pages/1002/roles?userId=bad")) {
            mvc.perform(get(ROOT + path).session(admin)).andExpect(status().isBadRequest());
        }
        for (String path :
                List.of(
                        "/users/999999/pages",
                        "/pages/999999/users",
                        "/pages/1001/users",
                        "/pages/1002/roles?userId=999999")) {
            mvc.perform(get(ROOT + path).session(admin)).andExpect(status().isNotFound());
        }
    }

    @Test
    void responsesContainOnlyMinimalIdentityAndReadsDoNotChangeBusinessRelations()
            throws Exception {
        jdbc.update(
                "UPDATE sys_user SET phone='123456789',email='private@example.test',avatar_key='private',failed_login_count=3 WHERE id=?",
                SUBJECT);
        List<Integer> before = counts();
        for (String path : ENDPOINTS) {
            if (path.endsWith(".xlsx")) {
                byte[] file =
                        mvc.perform(get(path).session(admin))
                                .andExpect(status().isOk())
                                .andReturn()
                                .getResponse()
                                .getContentAsByteArray();
                String sheet = workbookEntry(file, "xl/worksheets/sheet1.xml");
                assertThat(sheet).doesNotContain("123456789", "private@example.test", "private");
                continue;
            }
            Set<String> fields = new HashSet<>();
            fieldNames(read(path), fields);
            assertThat(fields)
                    .doesNotContain(
                            "password",
                            "phone",
                            "email",
                            "avatarKey",
                            "lockedUntil",
                            "sessionVersion",
                            "failedLoginCount",
                            "token",
                            "cookie");
        }
        assertThat(counts()).isEqualTo(before);
        Transactional transaction = AccessQueryService.class.getAnnotation(Transactional.class);
        assertThat(transaction.readOnly()).isTrue();
        assertThat(transaction.isolation()).isEqualTo(Isolation.REPEATABLE_READ);
    }

    private JsonNode userPage(boolean includeInactive) throws Exception {
        return read(
                ROOT + "/users/5010/pages?includeInactive=" + includeInactive + "&moduleKey=user");
    }

    private JsonNode read(String path) throws Exception {
        return data(
                mvc.perform(get(path).session(admin))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getResponse()
                        .getContentAsByteArray());
    }

    private JsonNode data(byte[] content) throws Exception {
        return json.readTree(content).path("data");
    }

    private MockHttpSession login(String username, String password) throws Exception {
        var csrfResult = mvc.perform(get("/auth/csrf")).andExpect(status().isOk()).andReturn();
        var csrf = data(csrfResult.getResponse().getContentAsByteArray());
        var anonymous = (MockHttpSession) csrfResult.getRequest().getSession(false);
        return (MockHttpSession)
                mvc.perform(
                                loginRequest(mvc, json, anonymous, username, password)
                                        .header(
                                                csrf.path("headerName").asText(),
                                                csrf.path("token").asText()))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getRequest()
                        .getSession(false);
    }

    private void addUser(long id, String name) {
        jdbc.update(
                "INSERT INTO sys_user(id,username,password,status,must_change_password) VALUES(?,?,?,1,FALSE)",
                id,
                name,
                hash);
    }

    private void addRole(long id, String code, boolean enabled) {
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status) VALUES(?,?,?,?)",
                id,
                code,
                code,
                enabled ? "ENABLED" : "DISABLED");
    }

    private void assign(long userId, long roleId, long menuId) {
        if (jdbc.queryForObject(
                        "SELECT COUNT(*) FROM sys_user_role WHERE user_id=? AND role_id=?",
                        Integer.class,
                        userId,
                        roleId)
                == 0) {
            jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,?)", userId, roleId);
        }
        grant(roleId, menuId);
    }

    private void grant(long roleId, long menuId) {
        if (jdbc.queryForObject(
                        "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=? AND menu_id=?",
                        Integer.class,
                        roleId,
                        menuId)
                == 0) {
            jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES(?,?)", roleId, menuId);
        }
    }

    private List<Integer> counts() {
        return List.of("sys_user", "sys_role", "sys_menu", "sys_user_role", "sys_role_menu")
                .stream()
                .map(table -> jdbc.queryForObject("SELECT COUNT(*) FROM " + table, Integer.class))
                .toList();
    }

    private static Set<String> codes(JsonNode blockers) {
        Set<String> result = new HashSet<>();
        blockers.forEach(row -> result.add(row.path("code").asText()));
        return result;
    }

    private static void fieldNames(JsonNode node, Set<String> result) {
        if (node.isObject()) node.fieldNames().forEachRemaining(result::add);
        if (node.isContainerNode()) node.forEach(child -> fieldNames(child, result));
    }
}
