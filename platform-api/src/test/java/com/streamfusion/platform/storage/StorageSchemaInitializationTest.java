package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatCode;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.support.IdentitySchema;
import java.util.UUID;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.boot.DefaultApplicationArguments;
import org.springframework.jdbc.core.JdbcTemplate;

class StorageSchemaInitializationTest {
    private JdbcTemplate sql;

    @BeforeEach
    void initializeFromReleaseSql() throws Exception {
        var dataSource = new JdbcDataSource();
        dataSource.setURL(
                "jdbc:h2:mem:storage_install_"
                        + UUID.randomUUID()
                        + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(dataSource);
        sql = new JdbcTemplate(dataSource);
    }

    @Test
    void completeInitializationProvidesStorageTablesAndSafeDefaults() {
        var check = new StorageSchemaCheck(sql);
        assertThatCode(() -> check.run(new DefaultApplicationArguments()))
                .doesNotThrowAnyException();
        assertThat(
                        sql.queryForObject(
                                "SELECT scope_id FROM storage_default WHERE id=1", Long.class))
                .isNull();
        assertThat(
                        sql.queryForObject(
                                "SELECT enabled FROM storage_history_policy WHERE id=0",
                                Integer.class))
                .isZero();
        assertThat(
                        sql.queryForObject(
                                "SELECT retention_days FROM storage_history_policy WHERE id=0",
                                Integer.class))
                .isNull();
        assertThat(sql.queryForObject("SELECT COUNT(*) FROM storage_instance", Integer.class))
                .isZero();
        assertThat(sql.queryForObject("SELECT COUNT(*) FROM storage_task", Integer.class)).isZero();
    }

    @Test
    void freshStoragePageIsGrantedToSuperAdmin() {
        assertThat(
                        sql.queryForList(
                                "SELECT r.code FROM sys_role_menu rm JOIN sys_role r ON r.id=rm.role_id WHERE rm.menu_id=1013",
                                String.class))
                .containsExactly("SUPER_ADMIN");
    }

    @Test
    void missingStorageSchemaFailsStartupWithoutRebuildingIt() {
        sql.execute("DROP TABLE storage_history_policy");
        assertThatThrownBy(() -> new StorageSchemaCheck(sql).run(new DefaultApplicationArguments()))
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining("sql/README.md");
        assertThat(
                        sql.queryForObject(
                                "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME='STORAGE_HISTORY_POLICY'",
                                Integer.class))
                .isZero();
    }
}
