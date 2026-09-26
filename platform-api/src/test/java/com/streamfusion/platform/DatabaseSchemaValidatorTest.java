package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.common.config.DatabaseSchemaValidator;
import com.streamfusion.platform.support.IdentitySchema;
import javax.sql.DataSource;
import org.apache.ibatis.session.SqlSessionFactory;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.ActiveProfiles;

@SpringBootTest(
        properties =
                "spring.datasource.url=jdbc:h2:mem:schema_validator;MODE=MySQL;DB_CLOSE_DELAY=-1")
@ActiveProfiles("test")
class DatabaseSchemaValidatorTest {
    @Autowired DataSource source;
    @Autowired SqlSessionFactory sessions;
    private DatabaseSchemaValidator validator;

    @BeforeEach
    void initialize() throws Exception {
        IdentitySchema.initialize(source);
        validator = new DatabaseSchemaValidator(source, sessions);
    }

    @Test
    void completeFreshSchemaPassesWithoutCreatingAnAccountOrChangingMenus() throws Exception {
        validator.validate();
        try (var connection = source.getConnection();
                var sql = connection.createStatement()) {
            try (var rows = sql.executeQuery("SELECT COUNT(*) FROM sys_user")) {
                rows.next();
                assertThat(rows.getInt(1)).isZero();
            }
            // Menu configuration is owned by administrators, not the startup checker.
            sql.executeUpdate("DELETE FROM sys_role_menu");
            sql.executeUpdate("UPDATE sys_menu SET enabled=FALSE");
        }
        validator.validate();
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
                "sys_user",
                "sys_role",
                "sys_menu",
                "sys_dept",
                "sys_operation_log",
                "sys_ip_block",
                "sys_login_record",
                "sys_user_role",
                "sys_user_dept",
                "sys_role_menu"
            })
    void everyMissingBusinessTableIsReportedBeforeLogin(String table) throws Exception {
        try (var connection = source.getConnection();
                var sql = connection.createStatement()) {
            sql.execute("DROP TABLE " + table + " CASCADE");
        }
        assertThatThrownBy(validator::validate)
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining(table)
                .hasMessageContaining("platform-api/sql/README.md");
    }

    @Test
    void missingEntityAndRelationshipColumnsAreReportedTogether() throws Exception {
        try (var connection = source.getConnection();
                var sql = connection.createStatement()) {
            sql.execute("ALTER TABLE sys_login_record DROP COLUMN nickname");
            sql.execute("ALTER TABLE sys_role_menu DROP COLUMN created_by");
        }
        assertThatThrownBy(validator::validate)
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining("sys_login_record.nickname")
                .hasMessageContaining("sys_role_menu.created_by");
    }
}
