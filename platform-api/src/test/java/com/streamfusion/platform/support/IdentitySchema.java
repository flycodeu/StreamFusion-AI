package com.streamfusion.platform.support;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.SQLException;
import javax.sql.DataSource;
import org.springframework.core.io.ByteArrayResource;
import org.springframework.core.io.ClassPathResource;
import org.springframework.core.io.support.EncodedResource;
import org.springframework.jdbc.datasource.init.ScriptUtils;

/** Executes the maintained schema in isolated H2 tests; this does not verify MySQL behavior. */
public final class IdentitySchema {
    private static final String MYSQL_USERNAME_COLUMN =
            "username VARCHAR(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci";

    private IdentitySchema() {}

    public static void initialize(DataSource dataSource) throws IOException, SQLException {
        try (Connection connection = dataSource.getConnection()) {
            initialize(connection);
        }
    }

    public static void initialize(Connection connection) throws IOException, SQLException {
        if (!"H2".equals(connection.getMetaData().getDatabaseProductName())) {
            throw new IllegalArgumentException(
                    "IdentitySchema only rebuilds isolated H2 databases");
        }
        String sql =
                new ClassPathResource("sql/汇总/streamfusion-mysql.sql")
                        .getContentAsString(StandardCharsets.UTF_8);
        // Adapt the username collation to H2's case-insensitive type. Keep its UNIQUE constraint.
        // CLOB preserves JSON text; H2's JSON JDBC writes otherwise encode it as a JSON string.
        // MySQL collation, JSON type and concurrency require separate MySQL verification.
        sql =
                sql.replace("SET NAMES utf8mb4;", "")
                        .replace("SET FOREIGN_KEY_CHECKS = 1;", "")
                        .replace("SET time_zone = '+08:00';", "SET TIME ZONE '+08:00';")
                        .replace("changes JSON", "changes CLOB")
                        .replace(MYSQL_USERNAME_COLUMN, "username VARCHAR_IGNORECASE(32)");
        ScriptUtils.executeSqlScript(
                connection,
                new EncodedResource(
                        new ByteArrayResource(sql.getBytes(StandardCharsets.UTF_8)),
                        StandardCharsets.UTF_8));
    }
}
