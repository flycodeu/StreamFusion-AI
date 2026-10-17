package com.streamfusion.platform.media.preview.node;

import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Component;

/** Explicit, read-only upgrade gate; no automatic schema mutation. */
@Component
@Profile("!test")
public final class PreviewNodeSchemaCheck implements ApplicationRunner {
    private final JdbcTemplate sql;

    public PreviewNodeSchemaCheck(JdbcTemplate sql) {
        this.sql = sql;
    }

    public void run(ApplicationArguments args) {
        try {
            sql.queryForList(
                    "SELECT id,node_id,name,control_uri,certificate_sha256,token_path,media_address,mode,config_version,version,priority,max_sources,max_viewers,max_transcodes,max_starting,ingress_kbps,egress_kbps,source_upper_kbps,output_upper_kbps FROM preview_node WHERE 1=0");
            sql.queryForList(
                    "SELECT viewer_id,node_id,boot_id,config_version,profile_id,connection_key,state,output_key,egress_kbps,is_starting FROM preview_viewer WHERE 1=0");
            sql.queryForList(
                    "SELECT profile_id,connection_key,node_id,config_version,source_codec,ingress_kbps FROM preview_source WHERE 1=0");
            sql.queryForList(
                    "SELECT profile_id,output_key,transcode_units FROM preview_output WHERE 1=0");
            sql.queryForList(
                    "SELECT operation_id,node_id,viewer_id,action,command_digest,deadline_at,state,reason_code FROM preview_operation WHERE 1=0");
        } catch (org.springframework.dao.DataAccessException failure) {
            throw new IllegalStateException(
                    "预览 S3 表结构缺失；备份后按预览节点部署说明执行五表增量升级，禁止重放初始化 SQL。", failure);
        }
    }
}
