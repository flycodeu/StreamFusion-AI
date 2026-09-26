package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;

import java.util.HashSet;
import java.util.Set;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.ActiveProfiles;

/** Opt-in, read-only metadata checks for an existing local schema. */
@SpringBootTest
@ActiveProfiles("local")
@EnabledIfSystemProperty(named = "sf.test.localSchema", matches = "true")
class LocalSchemaInspectionTest {
    private final DataSource source;

    @Autowired
    LocalSchemaInspectionTest(DataSource source) {
        this.source = source;
    }

    @Test
    void checksLocalProjectSchemaWithoutWrites() throws Exception {
        try (var connection = source.getConnection()) {
            assertThat(connection.getMetaData().getDatabaseProductName()).isEqualTo("MySQL");
            assertThat(connection.getCatalog()).isEqualTo("streamfusion");
            String url = connection.getMetaData().getURL();
            String endpoint = url.split("\\?", 2)[0];
            assertThat(endpoint)
                    .isIn(
                            "jdbc:mysql://127.0.0.1:3306/streamfusion",
                            "jdbc:mysql://localhost:3306/streamfusion");
        }
        try (var connection = source.getConnection();
                var sql = connection.createStatement()) {
            try (var rows = sql.executeQuery("SELECT @@session.time_zone")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("+08:00");
            }
            try (var rows =
                    sql.executeQuery("SELECT TIMESTAMPDIFF(SECOND, UTC_TIMESTAMP(), NOW())")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(28800);
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT column_comment FROM information_schema.columns WHERE table_schema=DATABASE() AND data_type='datetime' AND table_name LIKE 'sys_%'")) {
                int datetimeColumns = 0;
                while (rows.next()) {
                    assertThat(rows.getString(1)).isNotBlank();
                    datetimeColumns++;
                }
                assertThat(datetimeColumns).isEqualTo(13);
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT table_name, extra FROM information_schema.columns "
                                    + "WHERE table_schema=DATABASE() AND column_name='id' "
                                    + "AND table_name IN ('sys_user','sys_role','sys_menu',"
                                    + "'sys_dept','sys_operation_log')")) {
                int ids = 0;
                while (rows.next()) {
                    assertThat(rows.getString("extra")).doesNotContain("auto_increment");
                    ids++;
                }
                assertThat(ids).isEqualTo(5);
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name LIKE 'sys_%'")) {
                Set<String> tables = new HashSet<>();
                while (rows.next()) tables.add(rows.getString(1));
                assertThat(tables)
                        .containsExactlyInAnyOrder(
                                "sys_user",
                                "sys_role",
                                "sys_dept",
                                "sys_menu",
                                "sys_user_role",
                                "sys_user_dept",
                                "sys_role_menu",
                                "sys_operation_log");
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT table_name, column_name, column_comment FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name LIKE 'sys_%'")) {
                int count = 0;
                while (rows.next()) {
                    assertThat(rows.getString("column_comment"))
                            .as(
                                    "%s.%s comment",
                                    rows.getString("table_name"), rows.getString("column_name"))
                            .isNotBlank();
                    count++;
                }
                assertThat(count).isEqualTo(78);
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() "
                                    + "AND table_name IN ('sys_user','sys_role','sys_dept') "
                                    + "AND column_name IN ('is_deleted','username_active_key')")) {
                assertThat(rows.next()).isFalse();
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT collation_name FROM information_schema.columns WHERE table_schema=DATABASE() "
                                    + "AND table_name='sys_user' AND column_name='username'")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("utf8mb4_0900_as_ci");
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT column_name, non_unique FROM information_schema.statistics "
                                    + "WHERE table_schema=DATABASE() AND table_name='sys_user' "
                                    + "AND index_name='uq_user_username' ORDER BY seq_in_index")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getString(1)).isEqualTo("username");
                assertThat(rows.getInt(2)).isZero();
                assertThat(rows.next()).isFalse();
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT table_name, table_comment FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name LIKE 'sys_%'")) {
                while (rows.next()) {
                    assertThat(rows.getString("table_comment"))
                            .as("%s table comment", rows.getString("table_name"))
                            .isNotBlank();
                }
            }
        }
    }
}
