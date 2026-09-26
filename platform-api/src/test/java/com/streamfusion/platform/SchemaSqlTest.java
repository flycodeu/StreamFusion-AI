package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.support.IdentitySchema;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.core.io.ClassPathResource;

class SchemaSqlTest {
    private Connection connection;

    @BeforeEach
    void openIsolatedDatabase() throws Exception {
        connection = DriverManager.getConnection("jdbc:h2:mem:schema_sql;MODE=MySQL", "sa", "");
        IdentitySchema.initialize(connection);
    }

    @AfterEach
    void closeDatabase() throws Exception {
        if (connection != null) {
            connection.close();
        }
    }

    @Test
    void initializesAndRebuildsWithExampleOrganizationButWithoutSharedCredentials()
            throws Exception {
        assertInitialData();
        IdentitySchema.initialize(connection);
        assertInitialData();
    }

    private void assertInitialData() throws Exception {
        try (var sql = connection.createStatement()) {
            try (var rows = sql.executeQuery("SELECT id FROM sys_user")) {
                assertThat(rows.next()).isFalse();
            }
            assertThatThrownBy(() -> sql.executeQuery("SELECT password_hash FROM sys_user"))
                    .isInstanceOf(SQLException.class);
            try (var rows =
                    sql.executeQuery(
                            "SELECT user_id FROM sys_user_role ur JOIN sys_role r ON r.id=ur.role_id WHERE r.code='SUPER_ADMIN'")) {
                assertThat(rows.next()).isFalse();
            }
            try (var rows =
                    sql.executeQuery("SELECT id,parent_id,name FROM sys_dept ORDER BY id")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getLong("id")).isEqualTo(2001);
                assertThat(rows.getObject("parent_id")).isNull();
                assertThat(rows.getString("name")).isEqualTo("飞云科技公司");
                assertThat(rows.next()).isTrue();
                assertThat(rows.getLong("id")).isEqualTo(2002);
                assertThat(rows.getLong("parent_id")).isEqualTo(2001);
                assertThat(rows.getString("name")).isEqualTo("研发部门");
                assertThat(rows.next()).isTrue();
                assertThat(rows.getLong("id")).isEqualTo(2003);
                assertThat(rows.getLong("parent_id")).isEqualTo(2001);
                assertThat(rows.getString("name")).isEqualTo("运维部门");
                assertThat(rows.next()).isFalse();
            }
            try (var rows = sql.executeQuery("SELECT code FROM sys_role")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("SUPER_ADMIN");
                assertThat(rows.next()).isFalse();
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT COUNT(*) FROM sys_role_menu rm"
                                    + " JOIN sys_role r ON r.id=rm.role_id WHERE r.code='SUPER_ADMIN'")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(4);
            }
            assertThatThrownBy(() -> sql.executeQuery("SELECT * FROM sys_permission"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> sql.executeQuery("SELECT * FROM sys_role_permission"))
                    .isInstanceOf(SQLException.class);
        }
    }

    @Test
    void userDepartmentsAreManyToManyWithoutAPrimaryDepartmentColumn() throws Exception {
        insertUser(2103030953826844673L, "DepartmentUser", 1, 0);
        try (var sql = connection.createStatement()) {
            sql.executeUpdate(
                    "INSERT INTO sys_dept(id,name,sort_order,version) VALUES (2103030953826844701,'甲公司',0,0)");
            sql.executeUpdate(
                    "INSERT INTO sys_dept(id,name,sort_order,version) VALUES (2103030953826844702,'乙公司',1,0)");
            sql.executeUpdate(
                    "INSERT INTO sys_user_dept(user_id,dept_id) VALUES "
                            + "(2103030953826844673,2103030953826844701),"
                            + "(2103030953826844673,2103030953826844702)");
            try (var rows =
                    sql.executeQuery(
                            "SELECT COUNT(*) FROM sys_user_dept WHERE user_id=2103030953826844673")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(2);
            }
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "INSERT INTO sys_user_dept(user_id,dept_id) VALUES (2103030953826844673,2103030953826844701)"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> sql.executeQuery("SELECT dept_id FROM sys_user"))
                    .isInstanceOf(SQLException.class);
        }
    }

    @Test
    void repeatingSeedStatementsDoesNotResetAnExistingPasswordOrDuplicateBindings()
            throws Exception {
        insertUser(2103030953826844673L, "admin", 1, 0);
        try (var sql = connection.createStatement()) {
            sql.executeUpdate(
                    "UPDATE sys_user SET password='changed-test-hash',status=1,must_change_password=FALSE WHERE username='admin'");
            repeatDefaultSeeds();
            try (var rows =
                    sql.executeQuery(
                            "SELECT password,status,must_change_password FROM sys_user WHERE username='admin'")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString("password")).isEqualTo("changed-test-hash");
                assertThat(rows.getInt("status")).isEqualTo(1);
                assertThat(rows.getBoolean("must_change_password")).isFalse();
            }
            sql.executeUpdate("UPDATE sys_dept SET name='自定义公司' WHERE id=2001");
            repeatDefaultSeeds();
            try (var rows = sql.executeQuery("SELECT name FROM sys_dept WHERE id=2001")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("自定义公司");
            }
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_dept")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(3);
            }
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_user_role")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isZero();
            }
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_role_menu")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(4);
            }
        }
    }

    @Test
    void pageSeedGrantsNewPagesToSuperAdminWithoutDuplicateBindings() throws Exception {
        try (var sql = connection.createStatement()) {
            sql.executeUpdate(
                    "INSERT INTO sys_menu(id,name,type,route_name,path,component_key,module_key) "
                            + "VALUES (2103030953826844703,'新增页面','PAGE','AdditionalPage',"
                            + "'/additional','ADDITIONAL','additional')");
            repeatDefaultSeeds();
            repeatDefaultSeeds();
            try (var rows =
                    sql.executeQuery(
                            "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=1 AND menu_id=2103030953826844703")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(1);
            }
        }
    }

    @Test
    void seedDoesNotPromoteAnotherUserNamedAdmin() throws Exception {
        try (var sql = connection.createStatement()) {
            sql.executeUpdate("DELETE FROM sys_user_role");
            sql.executeUpdate("DELETE FROM sys_user");
            insertUser(700000000000000090L, "admin", 1, 0);
            repeatDefaultSeeds();
            try (var rows = sql.executeQuery("SELECT id,password FROM sys_user")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getLong("id")).isEqualTo(700000000000000090L);
                assertThat(rows.getString("password")).isEqualTo("test-only-hash");
                assertThat(rows.next()).isFalse();
            }
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_user_role")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isZero();
            }
        }
    }

    private void repeatDefaultSeeds() throws Exception {
        String sql =
                new ClassPathResource("sql/汇总/streamfusion-mysql.sql")
                        .getContentAsString(StandardCharsets.UTF_8);
        for (String table : new String[] {"sys_dept", "sys_role_menu"}) {
            int start = sql.indexOf("INSERT INTO " + table + " (");
            assertThat(start).isNotNegative();
            while (start >= 0) {
                int end = sql.indexOf(';', start);
                try (var statement = connection.createStatement()) {
                    statement.executeUpdate(sql.substring(start, end + 1));
                }
                start = sql.indexOf("INSERT INTO " + table + " (", end + 1);
            }
        }
    }

    @Test
    void requiresAnExplicitPositiveIdAndAllowsMissingNickname() throws Exception {
        try (var sql = connection.createStatement()) {
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "INSERT INTO sys_user(username,password) VALUES ('missingId','test-only-hash')"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> insertUser(0, "zeroId", 0, 0))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> insertUser(-1, "negativeId", 0, 0))
                    .isInstanceOf(SQLException.class);
            sql.executeUpdate(
                    "INSERT INTO sys_user(id,username,password) VALUES (700000000000000001,'schemaProbe','test-only-hash')");
            try (var rows =
                    sql.executeQuery(
                            "SELECT id,nickname,status,gender,must_change_password FROM sys_user WHERE id=700000000000000001")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getLong("id")).isEqualTo(700000000000000001L);
                assertThat(rows.getString("nickname")).isNull();
                assertThat(rows.getInt("status")).isZero();
                assertThat(rows.getInt("gender")).isZero();
                assertThat(rows.getBoolean("must_change_password")).isTrue();
            }
        }
    }

    @Test
    void acceptsOnlyDefinedStatusAndGenderValues() throws Exception {
        for (int value = 0; value <= 2; value++) {
            insertUser(700000000000000010L + value, "validUser" + value, value, value);
        }
        for (int invalid : new int[] {-1, 3}) {
            assertThatThrownBy(() -> insertUser(700000000000000020L, "invalidStatus", invalid, 0))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> insertUser(700000000000000021L, "invalidGender", 0, invalid))
                    .isInstanceOf(SQLException.class);
        }
    }

    @Test
    void rejectsDuplicateAndCaseVariantAccounts() throws Exception {
        insertUser(700000000000000030L, "SchemaUser", 0, 0);
        assertThatThrownBy(() -> insertUser(700000000000000031L, "SchemaUser", 1, 0))
                .isInstanceOf(SQLException.class);
        assertThatThrownBy(() -> insertUser(700000000000000032L, "schemauser", 1, 0))
                .isInstanceOf(SQLException.class);
    }

    @Test
    void permitsAccountReuseWithANewIdAfterHardDeletion() throws Exception {
        try (var sql = connection.createStatement()) {
            for (int generation = 0; generation < 3; generation++) {
                long id = 700000000000000040L + generation;
                insertUser(id, "ReusableUser", 1, 0);
                sql.executeUpdate("DELETE FROM sys_user WHERE id = " + id);
            }
            insertUser(700000000000000043L, "reusableuser", 0, 0);
            try (var rows =
                    sql.executeQuery("SELECT id FROM sys_user WHERE username='ReusableUser'")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getLong(1)).isEqualTo(700000000000000043L);
                assertThat(rows.next()).isFalse();
            }
            assertThatThrownBy(() -> sql.executeQuery("SELECT is_deleted FROM sys_user"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> sql.executeQuery("SELECT username_active_key FROM sys_user"))
                    .isInstanceOf(SQLException.class);
        }
    }

    @Test
    void rejectsInvalidRelationsAndProtectsSuperAdminRole() throws Exception {
        try (var sql = connection.createStatement()) {
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "INSERT INTO sys_user_role(user_id,role_id) VALUES (-1,-1)"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> sql.executeQuery("SELECT is_deleted FROM sys_role"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(() -> sql.executeQuery("SELECT is_deleted FROM sys_dept"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "UPDATE sys_role SET status='DISABLED' WHERE code='SUPER_ADMIN'"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "INSERT INTO sys_menu(name,type) VALUES ('Invalid page','PAGE')"))
                    .isInstanceOf(SQLException.class);
        }
    }

    private void insertUser(long id, String username, int status, int gender) throws SQLException {
        try (var sql =
                connection.prepareStatement(
                        "INSERT INTO sys_user(id,username,password,status,gender) VALUES (?,?,'test-only-hash',?,?)")) {
            sql.setLong(1, id);
            sql.setString(2, username);
            sql.setInt(3, status);
            sql.setInt(4, gender);
            sql.executeUpdate();
        }
    }
}
