package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.support.IdentitySchema;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.List;
import java.util.UUID;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.springframework.core.io.ByteArrayResource;
import org.springframework.core.io.support.EncodedResource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.init.ScriptUtils;

/** Additive upgrade rehearsal on disposable H2 only, never a connection from application config. */
class StorageUpgradeTest {
    @Test
    void historyPolicyUpgradePreservesExistingRecordsAndStartsDisabled() throws Exception {
        var dataSource = new JdbcDataSource();
        dataSource.setURL(
                "jdbc:h2:mem:history_upgrade_"
                        + UUID.randomUUID()
                        + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(dataSource);
        var sql = new JdbcTemplate(dataSource);
        sql.execute("DROP TABLE storage_history_policy");
        sql.update(
                "INSERT INTO storage_task(id,run_id,instance_id,actor_id,type,status,phase,request_key,request_fingerprint,created_at,finished_at) VALUES(7,7,1,1,'UPLOAD','SUCCEEDED','FINISHED','fixture','fixture',NOW(),NOW())");
        var before = sql.queryForList("SELECT * FROM storage_task");
        String upgrade = Files.readString(Path.of("sql/升级/20261022-storage-history-policy.sql"));
        assertThat(upgrade).doesNotContain("DROP TABLE", "TRUNCATE", "DELETE FROM");
        try (var connection = dataSource.getConnection()) {
            ScriptUtils.executeSqlScript(
                    connection,
                    new EncodedResource(
                            new ByteArrayResource(upgrade.getBytes(StandardCharsets.UTF_8)),
                            StandardCharsets.UTF_8));
        }
        assertThat(sql.queryForList("SELECT * FROM storage_task")).isEqualTo(before);
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
    }

    @Test
    void sevenTablesAndNewPageAreAddedWithoutChangingExistingAuthorization() throws Exception {
        var dataSource = new JdbcDataSource();
        dataSource.setURL(
                "jdbc:h2:mem:storage_upgrade_"
                        + UUID.randomUUID()
                        + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(dataSource);
        var sql = new JdbcTemplate(dataSource);
        for (String table :
                List.of(
                        "storage_cleanup_item",
                        "storage_cleanup_plan",
                        "storage_cleanup_rule",
                        "storage_task",
                        "storage_default",
                        "storage_scope",
                        "storage_instance")) sql.execute("DROP TABLE " + table);
        sql.update("DELETE FROM sys_role_menu WHERE menu_id=1013");
        sql.update("DELETE FROM sys_menu WHERE id=1013");
        int previousPages = sql.queryForObject("SELECT COUNT(*) FROM sys_menu", Integer.class);
        sql.update(
                "INSERT INTO sys_role(id,code,name,status) VALUES(42,'ORDINARY_STORAGE_TEST','普通测试角色','ENABLED')");
        String upgrade = Files.readString(Path.of("sql/升级/20261022-storage-management.sql"));
        assertThat(upgrade)
                .doesNotContain("DROP TABLE", "TRUNCATE", "DELETE FROM", "FOREIGN_KEY_CHECKS = 0");
        upgrade =
                upgrade.replace("SET NAMES utf8mb4;", "")
                        .replace("SET time_zone = '+08:00';", "SET TIME ZONE '+08:00';");
        try (var connection = dataSource.getConnection()) {
            ScriptUtils.executeSqlScript(
                    connection,
                    new EncodedResource(
                            new ByteArrayResource(upgrade.getBytes(StandardCharsets.UTF_8)),
                            StandardCharsets.UTF_8));
        }
        assertThat(sql.queryForObject("SELECT COUNT(*) FROM sys_menu", Integer.class))
                .isEqualTo(previousPages + 1);
        assertThat(
                        sql.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=1 AND menu_id=1013",
                                Integer.class))
                .isOne();
        assertThat(
                        sql.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=42",
                                Integer.class))
                .isZero();
        assertThat(
                        sql.queryForObject(
                                "SELECT scope_id FROM storage_default WHERE id=1", Long.class))
                .isNull();
        for (String table :
                List.of(
                        "storage_instance",
                        "storage_scope",
                        "storage_task",
                        "storage_cleanup_plan",
                        "storage_cleanup_item",
                        "storage_cleanup_rule"))
            assertThat(sql.queryForObject("SELECT COUNT(*) FROM " + table, Integer.class)).isZero();
        String consoleUpgrade = Files.readString(Path.of("sql/升级/20261022-storage-console.sql"));
        assertThat(consoleUpgrade).doesNotContain("DROP TABLE", "DELETE FROM", "TRUNCATE");
        try (var connection = dataSource.getConnection()) {
            ScriptUtils.executeSqlScript(
                    connection,
                    new EncodedResource(
                            new ByteArrayResource(consoleUpgrade.getBytes(StandardCharsets.UTF_8)),
                            StandardCharsets.UTF_8));
        }
        assertThat(sql.queryForList("SELECT management_url FROM storage_instance")).isEmpty();
        assertThat(sql.queryForObject("SELECT COUNT(*) FROM sys_menu", Integer.class))
                .isEqualTo(previousPages + 1);
    }
}
