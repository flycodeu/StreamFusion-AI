package com.streamfusion.platform;

import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.ActiveProfiles;

@SpringBootTest
@ActiveProfiles("test")
class SchemaMigrationTest {
    private final DataSource source;
    private final Flyway flyway;

    @Autowired
    SchemaMigrationTest(DataSource source, Flyway flyway) {
        this.source = source;
        this.flyway = flyway;
    }

    @Test
    void appliesAllMigrationsOnceWithoutCreatingCredentials() throws Exception {
        SchemaAssertions.assertFreshInstallation(source, flyway);
    }

    @Test
    void databaseRejectsInvalidRelationsAndDuplicateIdentities() throws Exception {
        SchemaAssertions.assertConstraints(source);
    }
}
