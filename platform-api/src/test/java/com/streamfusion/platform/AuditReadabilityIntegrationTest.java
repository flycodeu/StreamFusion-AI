package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.audit.mapper.AuditReadMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditCriteria;
import com.streamfusion.platform.audit.pojo.dto.AuditQueryDto;
import com.streamfusion.platform.audit.service.AuditQueryService;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.support.IdentitySchema;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.slf4j.MDC;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.context.ActiveProfiles;

@SpringBootTest(
        properties =
                "spring.datasource.url=jdbc:h2:mem:audit_readability;MODE=MySQL;DB_CLOSE_DELAY=-1")
@ActiveProfiles("test")
class AuditReadabilityIntegrationTest {
    @Autowired DataSource source;
    @Autowired AuditService audit;
    @Autowired AuditQueryService queries;
    @Autowired AuditReadMapper readMapper;
    private JdbcTemplate jdbc;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password) VALUES(101,'AuditActor','操作时用户','synthetic-only')");
        jdbc.update("INSERT INTO sys_role(id,name,code) VALUES(102,'操作时角色','ROLE_OLD')");
        jdbc.update("INSERT INTO sys_dept(id,name) VALUES(103,'操作时部门')");
        jdbc.update(
                "INSERT INTO sys_menu(id,name,type,route_name,path,component_key,module_key) VALUES(104,'操作时菜单','PAGE','auditFixture','/fixture','/fixture','audit')");
    }

    @Test
    void keepsRelationshipActorAndTargetSnapshotsAfterRenameAndHardDelete() {
        String id =
                record(
                        "USER",
                        101L,
                        "RELATIONS",
                        Map.of(
                                "beforeRoleIds",
                                List.of("102"),
                                "afterRoleIds",
                                List.of(),
                                "beforeDepartmentIds",
                                List.of("103"),
                                "afterDepartmentIds",
                                List.of(),
                                "beforeMenuIds",
                                List.of("104"),
                                "afterMenuIds",
                                List.of()));
        jdbc.update("UPDATE sys_user SET nickname='目前用户' WHERE id=101");
        jdbc.update("UPDATE sys_role SET name='目前角色',code='ROLE_NEW' WHERE id=102");
        jdbc.update("DELETE FROM sys_dept WHERE id=103");
        jdbc.update("DELETE FROM sys_menu WHERE id=104");
        var detail = queries.detail(id);
        assertThat(detail.record().actor().name()).isEqualTo("操作时用户");
        assertThat(detail.record().actor().source()).isEqualTo("SNAPSHOT");
        assertThat(detail.record().target().name()).isEqualTo("操作时用户");
        assertThat(detail.relations().get("beforeRoleIds").getFirst().name()).isEqualTo("操作时角色");
        assertThat(detail.relations().get("beforeRoleIds").getFirst().code()).isEqualTo("ROLE_OLD");
        assertThat(detail.relations().get("beforeDepartmentIds").getFirst().name())
                .isEqualTo("操作时部门");
        assertThat(detail.relations().get("beforeMenuIds").getFirst().name()).isEqualTo("操作时菜单");
        assertThat(detail.relations().get("beforeMenuIds").getFirst().source())
                .isEqualTo("SNAPSHOT");
        assertThat(detail.relations().get("afterMenuIds")).isEmpty();
        assertThat(detail.changes()).doesNotContainKeys("_actor", "_target", "_relations");
        assertThat(queries.page(new AuditQueryDto()).getItems().getFirst().actor().name())
                .isEqualTo("目前用户");
        assertThat(queries.page(new AuditQueryDto()).getItems().getFirst().actor().source())
                .isEqualTo("CURRENT");
        jdbc.update("DELETE FROM sys_user WHERE id=101");
        assertThat(queries.detail(id).record().actor().name()).isEqualTo("操作时用户");
        assertThat(queries.page(new AuditQueryDto()).getItems().getFirst().actor().source())
                .isEqualTo("MISSING");
    }

    @Test
    void historicalIdsUseCurrentOrMissingLabelsWithoutRewritingHistory() {
        String original =
                "{\"beforeRoleIds\":[\"102\",\"999\"],\"parentId\":\"103\",\"password\":\"must-not-expose\"}";
        jdbc.update(
                "INSERT INTO sys_operation_log(id,actor_id,target_type,target_id,action,result,changes) VALUES(110,101,'DEPT',103,'OLD','SUCCESS',?)",
                original);
        jdbc.update("UPDATE sys_role SET name='当前角色',code='CURRENT_ROLE' WHERE id=102");
        var detail = queries.detail("110");
        var known = detail.relations().get("beforeRoleIds").getFirst();
        assertThat(known.name()).isEqualTo("当前角色");
        assertThat(known.code()).isEqualTo("CURRENT_ROLE");
        assertThat(known.source()).isEqualTo("CURRENT");
        var absent = detail.relations().get("beforeRoleIds").getLast();
        assertThat(absent.id()).isEqualTo("999");
        assertThat(absent.source()).isEqualTo("MISSING");
        assertThat(absent.name()).isNull();
        assertThat(detail.relations().get("parentId").getFirst().type()).isEqualTo("DEPT");
        assertThat(detail.record().actor().source()).isEqualTo("CURRENT");
        assertThat(detail.changes()).doesNotContainKey("password");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT changes FROM sys_operation_log WHERE id=110", String.class))
                .isEqualTo(original);
    }

    @Test
    void capturesDeletedTargetSummaryAndScalarParentRelationships() {
        jdbc.update("DELETE FROM sys_menu WHERE id=104");
        String id =
                record(
                        "MENU",
                        104L,
                        "MENU_DELETE",
                        Map.of("name", "已删除菜单", "code", "deletedMenu", "parentId", "1001"));
        jdbc.update("UPDATE sys_menu SET name='父目录已改名' WHERE id=1001");
        var detail = queries.detail(id);
        assertThat(detail.record().target().name()).isEqualTo("已删除菜单");
        assertThat(detail.record().target().code()).isEqualTo("deletedMenu");
        assertThat(detail.record().target().source()).isEqualTo("SNAPSHOT");
        assertThat(detail.relations().get("parentId").getFirst().name()).isEqualTo("系统管理");
        assertThat(detail.relations().get("parentId").getFirst().source()).isEqualTo("SNAPSHOT");
        Map<String, Object> root = new HashMap<>();
        root.put("name", "根部门");
        root.put("parentId", null);
        String rootId = record("DEPT", 103L, "ROOT", root);
        assertThat(queries.detail(rootId).relations().get("parentId")).isEmpty();
    }

    @Test
    void exactTraceQueryCorrelatesEventsAndPageDoesNotLoadChanges() {
        String trace = "abcdef0123456789abcdef0123456789";
        try (var ignored = MDC.putCloseable("traceId", trace)) {
            record("USER", 101L, "TRACE_ONE", Map.of());
            record("ROLE", 102L, "TRACE_TWO", Map.of());
        }
        try (var ignored = MDC.putCloseable("traceId", "bbcdef0123456789abcdef0123456789")) {
            record("USER", 101L, "OTHER", Map.of());
        }
        AuditQueryDto query = new AuditQueryDto();
        query.setTraceId("  " + trace.toUpperCase(java.util.Locale.ROOT) + "  ");
        assertThat(queries.page(query).getTotal()).isEqualTo(2);
        query.setAction("TRACE_TWO");
        assertThat(queries.page(query).getTotal()).isEqualTo(1);
        query.setTraceId(trace.substring(1));
        assertThatThrownBy(() -> queries.page(query)).isInstanceOf(BusinessException.class);
        var rows =
                readMapper.page(
                        new Page<>(1, 20),
                        new AuditCriteria(null, null, null, null, null, trace, null, null));
        assertThat(rows.getRecords())
                .hasSize(2)
                .allSatisfy(row -> assertThat(row.getChanges()).isNull());
    }

    private String record(String type, long targetId, String action, Map<String, ?> changes) {
        audit.record(101L, type, targetId, action, "SUCCESS", null, null, changes);
        return jdbc.queryForObject(
                        "SELECT id FROM sys_operation_log WHERE action=?", Long.class, action)
                .toString();
    }
}
