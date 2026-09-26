package com.streamfusion.platform.common.config;

import com.baomidou.mybatisplus.core.metadata.TableInfo;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import java.sql.Connection;
import java.sql.SQLException;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.TreeMap;
import javax.sql.DataSource;
import org.apache.ibatis.session.SqlSessionFactory;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;

/** Read-only startup check; schema installation and upgrades remain explicit operations. */
@Component
@Profile("!test")
@Order(Ordered.HIGHEST_PRECEDENCE)
public class DatabaseSchemaValidator implements ApplicationRunner {
    private final DataSource source;
    private final Map<String, Set<String>> required = new TreeMap<>();

    public DatabaseSchemaValidator(DataSource source, SqlSessionFactory sessions) {
        this.source = source;
        // Use this factory's mapped entities, not a second list of entity column definitions.
        var configuration = sessions.getConfiguration();
        configuration.getMappedStatementNames().stream()
                .filter(name -> name.contains("."))
                .map(configuration::getMappedStatement)
                .flatMap(statement -> statement.getResultMaps().stream())
                .forEach(
                        result -> {
                            TableInfo table = TableInfoHelper.getTableInfo(result.getType());
                            if (table == null
                                    || !table.getEntityType()
                                            .getPackageName()
                                            .startsWith("com.streamfusion.platform.")) return;
                            Set<String> columns =
                                    required.computeIfAbsent(
                                            table.getTableName(), key -> new LinkedHashSet<>());
                            if (table.havePK()) columns.add(table.getKeyColumn());
                            table.getFieldList().forEach(field -> columns.add(field.getColumn()));
                        });
        if (required.isEmpty()) {
            throw new IllegalStateException("数据库结构检查未能读取实体映射，请检查 MyBatis 配置。");
        }
        // Relationship tables use explicit XML writes and have no MyBatis-Plus entity.
        required.put("sys_user_role", Set.of("user_id", "role_id", "created_at", "created_by"));
        required.put("sys_user_dept", Set.of("user_id", "dept_id", "created_at", "created_by"));
        required.put("sys_role_menu", Set.of("role_id", "menu_id", "created_at", "created_by"));
    }

    @Override
    public void run(ApplicationArguments arguments) {
        validate();
    }

    public void validate() {
        List<String> missing = new ArrayList<>();
        try (Connection connection = source.getConnection()) {
            Set<String> tables = new LinkedHashSet<>();
            try (var rows =
                    connection
                            .getMetaData()
                            .getTables(
                                    connection.getCatalog(),
                                    connection.getSchema(),
                                    "%",
                                    new String[] {"TABLE", "BASE TABLE"})) {
                while (rows.next())
                    tables.add(rows.getString("TABLE_NAME").toLowerCase(Locale.ROOT));
            }
            for (var entry : required.entrySet()) {
                String table = entry.getKey();
                if (!tables.contains(table)) {
                    missing.add(table);
                    continue;
                }
                // Identifiers come exclusively from mapped code, never a request or configuration.
                try (var statement = connection.createStatement()) {
                    statement.setQueryTimeout(5);
                    try (var rows =
                            statement.executeQuery("SELECT * FROM " + table + " WHERE 1=0")) {
                        Set<String> columns = new LinkedHashSet<>();
                        var metadata = rows.getMetaData();
                        for (int index = 1; index <= metadata.getColumnCount(); index++) {
                            columns.add(metadata.getColumnName(index).toLowerCase(Locale.ROOT));
                        }
                        entry.getValue().stream()
                                .filter(column -> !columns.contains(column))
                                .sorted()
                                .forEach(column -> missing.add(table + "." + column));
                    }
                }
            }
        } catch (SQLException exception) {
            throw new IllegalStateException(
                    "数据库结构检查失败，请核对数据库连接及读取权限（SQLState="
                            + exception.getSQLState()
                            + "）。参见 platform-api/sql/README.md。");
        }
        if (!missing.isEmpty()) {
            throw new IllegalStateException(
                    "数据库结构不完整，缺少："
                            + String.join("、", missing)
                            + "。请按 platform-api/sql/README.md 初始化空库或备份后增量升级已有库；"
                            + "禁止在已有数据的库重放初始化脚本。");
        }
    }
}
