package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import javax.sql.DataSource;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Select;
import org.apache.ibatis.session.SqlSessionFactory;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.data.redis.connection.lettuce.LettuceConnectionFactory;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.test.context.ActiveProfiles;

@SpringBootTest
@ActiveProfiles("test")
class InfrastructureConfigurationTest {
    private final DataSource dataSource;
    private final SqlSessionFactory sessions;
    private final ProbeMapper mapper;
    private final StringRedisTemplate redis;

    @Autowired
    InfrastructureConfigurationTest(
            DataSource dataSource,
            SqlSessionFactory sessions,
            ProbeMapper mapper,
            StringRedisTemplate redis) {
        this.dataSource = dataSource;
        this.sessions = sessions;
        this.mapper = mapper;
        this.redis = redis;
    }

    @Mapper
    interface ProbeMapper {
        @Select("SELECT 1")
        int ping();
    }

    @Test
    void usesMybatisPlusAndTestDatabaseWithoutCreatingBusinessTables() throws Exception {
        assertThat(sessions.getConfiguration()).isInstanceOf(MybatisConfiguration.class);
        assertThat(mapper.ping()).isEqualTo(1);
        try (var connection = dataSource.getConnection()) {
            assertThat(connection.getMetaData().getURL()).startsWith("jdbc:h2:mem:");
        }
    }

    @Test
    void configuresRedisWithoutConnectingToThePersonalDatabase() {
        var connection = (LettuceConnectionFactory) redis.getConnectionFactory();
        assertThat(connection).isNotNull();
        assertThat(connection.getDatabase()).isZero();
        assertThat(connection.getPort()).isEqualTo(16379);
    }
}
