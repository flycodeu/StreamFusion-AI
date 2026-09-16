package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;

import javax.sql.DataSource;
import org.apache.ibatis.session.SqlSessionFactory;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.test.context.ActiveProfiles;

/** Explicit opt-in, read-only local connectivity test. Never writes keys, tables or schema. */
@SpringBootTest
@ActiveProfiles("local")
@EnabledIfSystemProperty(named = "sf.test.localInfrastructure", matches = "true")
class LocalInfrastructureTest {
    private final DataSource dataSource;
    private final StringRedisTemplate redis;
    private final SqlSessionFactory sessions;

    @Autowired
    LocalInfrastructureTest(
            DataSource dataSource, StringRedisTemplate redis, SqlSessionFactory sessions) {
        this.dataSource = dataSource;
        this.redis = redis;
        this.sessions = sessions;
    }

    @Test
    void connectsToMysql() throws Exception {
        try (var session = sessions.openSession()) {
            assertThat(session.getMapper(InfrastructureConfigurationTest.ProbeMapper.class).ping())
                    .isEqualTo(1);
        }
        try (var connection = dataSource.getConnection();
                var statement = connection.createStatement()) {
            statement.setQueryTimeout(3);
            try (var rows = statement.executeQuery("SELECT 1")) {
                assertThat(rows.next()).isTrue();
                assertThat(rows.getInt(1)).isEqualTo(1);
                assertThat(connection.getMetaData().getDatabaseProductName()).isEqualTo("MySQL");
            }
        }
    }

    @Test
    void connectsToRedis() {
        try (var connection = redis.getConnectionFactory().getConnection()) {
            assertThat(connection.ping()).isEqualTo("PONG");
        }
    }
}
