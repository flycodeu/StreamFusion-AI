package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import java.sql.SQLException;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;

final class SchemaAssertions {
    private SchemaAssertions() {}

    static void assertFreshInstallation(DataSource source, Flyway flyway) throws Exception {
        assertThat(flyway.info().applied()).hasSize(11);
        assertThat(flyway.migrate().migrationsExecuted).isZero();
        try (var connection = source.getConnection();
                var sql = connection.createStatement()) {
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_user")) {
                rows.next();
                assertThat(rows.getInt(1)).isZero();
            }
            try (var rows = sql.executeQuery("SELECT code FROM sys_role")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("SUPER_ADMIN");
                assertThat(rows.next()).isFalse();
            }
        }
    }

    static void assertConstraints(DataSource source) throws Exception {
        try (var connection = source.getConnection();
                var sql = connection.createStatement()) {
            connection.setAutoCommit(false);
            try {
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
            } finally {
                connection.rollback();
            }
        }
    }
}
