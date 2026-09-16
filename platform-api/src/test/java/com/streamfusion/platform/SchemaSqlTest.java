package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.core.io.ByteArrayResource;
import org.springframework.core.io.ClassPathResource;
import org.springframework.core.io.support.EncodedResource;
import org.springframework.jdbc.datasource.init.ScriptUtils;

class SchemaSqlTest {
    private Connection connection;

    @BeforeEach
    void openIsolatedDatabase() throws Exception {
        connection = DriverManager.getConnection("jdbc:h2:mem:schema_sql;MODE=MySQL", "sa", "");
        initialize();
    }

    @AfterEach
    void closeDatabase() throws Exception {
        if (connection != null) {
            connection.close();
        }
    }

    private void initialize() throws Exception {
        String sql =
                new ClassPathResource("sql/汇总/streamfusion-mysql.sql")
                        .getContentAsString(StandardCharsets.UTF_8);
        // Only adapt MySQL session settings; exercise the actual DDL and seed data unchanged.
        sql =
                sql.replace("SET NAMES utf8mb4;", "")
                        .replace("SET FOREIGN_KEY_CHECKS = 1;", "")
                        .replace("SET time_zone = '+08:00';", "SET TIME ZONE '+08:00';");
        ScriptUtils.executeSqlScript(
                connection,
                new EncodedResource(
                        new ByteArrayResource(sql.getBytes(StandardCharsets.UTF_8)),
                        StandardCharsets.UTF_8));
    }

    @Test
    void initializesAndRebuildsWithoutCreatingCredentials() throws Exception {
        assertInitialData();
        initialize();
        assertInitialData();
    }

    private void assertInitialData() throws Exception {
        try (var sql = connection.createStatement()) {
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_user")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isZero();
            }
            try (var rows = sql.executeQuery("SELECT code FROM sys_role")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("SUPER_ADMIN");
                assertThat(rows.next()).isFalse();
            }
        }
    }

    @Test
    void rejectsInvalidRelationsAndDuplicateIdentities() throws Exception {
        try (var sql = connection.createStatement()) {
            sql.executeUpdate(
                    "INSERT INTO sys_user(username,password_hash,nickname) VALUES ('schema_probe','test-only-hash','Probe')");
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "INSERT INTO sys_user(username,password_hash,nickname) VALUES ('schema_probe','test-only-hash','Duplicate')"))
                    .isInstanceOf(SQLException.class);
            assertThatThrownBy(
                            () ->
                                    sql.executeUpdate(
                                            "INSERT INTO sys_user_role(user_id,role_id) VALUES (-1,-1)"))
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
}
