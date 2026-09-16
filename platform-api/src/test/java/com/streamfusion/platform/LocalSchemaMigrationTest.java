package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;

import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.ActiveProfiles;

/** Explicitly writes migrations ONLY to the loopback streamfusion development database. */
@SpringBootTest(properties = "spring.flyway.enabled=false")
@ActiveProfiles("local")
@EnabledIfSystemProperty(named = "sf.test.localSchema", matches = "true")
class LocalSchemaMigrationTest {
    private final DataSource source;

    @Autowired
    LocalSchemaMigrationTest(DataSource source) {
        this.source = source;
    }

    @Test
    void migratesAndChecksLocalProjectSchema() throws Exception {
        try (var connection = source.getConnection()) {
            assertThat(connection.getMetaData().getDatabaseProductName()).isEqualTo("MySQL");
            assertThat(connection.getCatalog()).isEqualTo("streamfusion");
            String url = connection.getMetaData().getURL();
            String endpoint = url.split("\\?", 2)[0];
            assertThat(endpoint)
                    .isIn(
                            "jdbc:mysql://127.0.0.1:3306/streamfusion",
                            "jdbc:mysql://localhost:3306/streamfusion");
            try (var tables =
                    connection.getMetaData().getTables("streamfusion", null, "sys_user", null)) {
                if (tables.next()) {
                    try (var statement = connection.createStatement();
                            var users = statement.executeQuery("SELECT COUNT(*) FROM sys_user")) {
                        assertThat(users.next()).isTrue();
                        assertThat(users.getLong(1))
                                .as("Fresh-install test refuses a database with users")
                                .isZero();
                    }
                }
            }
        }
        var flyway =
                Flyway.configure()
                        .dataSource(source)
                        .locations("classpath:db/migration")
                        .validateMigrationNaming(true)
                        .baselineOnMigrate(false)
                        .cleanDisabled(true)
                        .load();
        flyway.migrate();
        flyway.validate();
        SchemaAssertions.assertFreshInstallation(source, flyway);
        SchemaAssertions.assertConstraints(source);
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
                    assertThat(rows.getString(1)).contains("UTC+8");
                    datetimeColumns++;
                }
                assertThat(datetimeColumns).isEqualTo(12);
            }
            try (var rows =
                    sql.executeQuery(
                            "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name LIKE 'sys_%'")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(8);
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
                assertThat(count).isEqualTo(79);
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
