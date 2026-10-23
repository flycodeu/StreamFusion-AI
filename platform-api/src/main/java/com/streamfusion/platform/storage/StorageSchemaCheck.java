package com.streamfusion.platform.storage;

import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Component;

/** Read-only startup gate. Operators explicitly apply the additive upgrade after backup. */
@Component
@Profile("!test")
public class StorageSchemaCheck implements ApplicationRunner {
    private final JdbcTemplate sql;

    public StorageSchemaCheck(JdbcTemplate sql) {
        this.sql = sql;
    }

    @Override
    public void run(ApplicationArguments args) {
        try {
            sql.queryForList(
                    "SELECT id,inherit_global,enabled,retention_days,version FROM storage_history_policy WHERE 1=0");
            if (sql.queryForObject(
                            "SELECT COUNT(*) FROM storage_history_policy WHERE id=0", Integer.class)
                    != 1) throw new IllegalStateException("图床全局记录保留策略缺失，请核对定向升级");
            sql.queryForList(
                    "SELECT id,endpoint,management_url,secret_ciphertext,observation,usage_snapshot,version FROM storage_instance WHERE 1=0");
            sql.queryForList(
                    "SELECT id,instance_id,bucket,prefix,identity_digest,version FROM storage_scope WHERE 1=0");
            sql.queryForList("SELECT id,scope_id,version FROM storage_default WHERE id=1");
            if (sql.queryForObject("SELECT COUNT(*) FROM storage_default WHERE id=1", Integer.class)
                    != 1) throw new IllegalStateException("图床默认位置记录缺失，请核对定向升级");
            sql.queryForList(
                    "SELECT id,run_id,input_json,result_json,cursor_json,owner,lease_until,version FROM storage_task WHERE 1=0");
            sql.queryForList(
                    "SELECT id,scope_id,input_json,expires_at,version FROM storage_cleanup_plan WHERE 1=0");
            sql.queryForList(
                    "SELECT id,plan_id,object_key,etag,status FROM storage_cleanup_item WHERE 1=0");
            sql.queryForList(
                    "SELECT id,scope_id,enabled,next_at,version FROM storage_cleanup_rule WHERE 1=0");
        } catch (org.springframework.dao.DataAccessException failure) {
            throw new IllegalStateException(
                    "图床结构缺失；备份后核对 sql/升级/20261022-storage-management.sql、20261022-storage-console.sql、20261022-storage-history-policy.sql，禁止重放初始化 SQL。",
                    failure);
        }
    }
}
