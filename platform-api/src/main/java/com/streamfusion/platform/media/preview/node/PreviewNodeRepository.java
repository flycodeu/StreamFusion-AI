package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.control.PreviewControl.Command;
import com.streamfusion.platform.media.preview.control.PreviewControl.Receipt;
import java.net.URI;
import java.nio.file.Path;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.List;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Repository;

/** Node identities and durable viewer/operation ownership for one scheduling platform. */
@Repository
public class PreviewNodeRepository {
    public record Node(
            PreviewNodeConnection connection,
            String mode,
            String configVersion,
            long version,
            PreviewBudget budget,
            int priority,
            ZlmDirectConnection zlm,
            OpsNodeSource opsSource) {
        public Node(
                PreviewNodeConnection connection,
                String mode,
                String configVersion,
                long version,
                PreviewBudget budget,
                int priority,
                ZlmDirectConnection zlm) {
            this(connection, mode, configVersion, version, budget, priority, zlm, null);
        }

        public Node(
                PreviewNodeConnection connection, String mode, String configVersion, long version) {
            this(
                    connection,
                    mode,
                    configVersion,
                    version,
                    PreviewBudget.defaults(64, 64),
                    100,
                    null);
        }

        public Node(
                PreviewNodeConnection connection,
                String mode,
                String configVersion,
                long version,
                PreviewBudget budget,
                int priority) {
            this(connection, mode, configVersion, version, budget, priority, null);
        }

        public String nodeId() {
            return zlm != null ? zlm.nodeId() : connection == null ? "local" : connection.nodeId();
        }

        public boolean local() {
            return connection == null && zlm == null;
        }

        public String kind() {
            return local() ? "LOCAL" : zlm != null ? "ZLM_DIRECT" : "MANAGED";
        }
    }

    public record Viewer(
            String id, String nodeId, String bootId, String configVersion, String state) {}

    private final JdbcTemplate sql;
    private final com.streamfusion.platform.media.preview.PreviewProperties localLimits;

    public PreviewNodeRepository(JdbcTemplate sql) {
        this(sql, new com.streamfusion.platform.media.preview.PreviewProperties(64, 64));
    }

    @org.springframework.beans.factory.annotation.Autowired
    public PreviewNodeRepository(
            JdbcTemplate sql, com.streamfusion.platform.media.preview.PreviewProperties limits) {
        this.sql = sql;
        this.localLimits = limits;
    }

    public Node node() {
        return nodes().stream().filter(n -> n.connection() != null).findFirst().orElse(null);
    }

    public Node node(String id) {
        return nodes().stream().filter(n -> n.nodeId().equals(id)).findFirst().orElse(null);
    }

    public List<Node> nodes() {
        return sql.query(
                "SELECT * FROM preview_node ORDER BY id LIMIT 17",
                (row, i) ->
                        new Node(
                                !"MANAGED".equals(row.getString("node_kind"))
                                        ? null
                                        : new PreviewNodeConnection(
                                                row.getString("node_id"),
                                                row.getString("name"),
                                                URI.create(row.getString("control_uri")),
                                                row.getString("certificate_sha256"),
                                                Path.of(row.getString("token_path")),
                                                row.getString("media_address")),
                                row.getString("mode"),
                                row.getString("config_version"),
                                row.getLong("version"),
                                new PreviewBudget(
                                        row.getInt("max_sources"),
                                        row.getString("node_id").equals("local")
                                                ? Math.min(
                                                        localLimits.maxSessions(),
                                                        row.getInt("max_viewers"))
                                                : row.getInt("max_viewers"),
                                        row.getString("node_id").equals("local")
                                                ? Math.min(
                                                        localLimits.maxTranscodes(),
                                                        row.getInt("max_transcodes"))
                                                : row.getInt("max_transcodes"),
                                        row.getInt("max_starting"),
                                        row.getLong("ingress_kbps"),
                                        row.getLong("egress_kbps"),
                                        row.getLong("source_upper_kbps"),
                                        row.getLong("output_upper_kbps")),
                                row.getInt("priority"),
                                "ZLM_DIRECT".equals(row.getString("node_kind"))
                                        ? new ZlmDirectConnection(
                                                row.getString("node_id"),
                                                row.getString("name"),
                                                URI.create(row.getString("control_uri")),
                                                Path.of(row.getString("token_path")),
                                                row.getString("media_address"))
                                        : null,
                                source(row)));
    }

    private OpsNodeSource source(ResultSet row) throws SQLException {
        if (row.getString("ops_controller_id") == null) return null;
        return new OpsNodeSource(
                row.getString("ops_controller_id"),
                row.getString("ops_target_id"),
                row.getString("ops_instance_id"),
                row.getString("ops_release_id"),
                row.getTimestamp("ops_synced_at")
                        .toLocalDateTime()
                        .atZone(java.time.ZoneId.of("Asia/Shanghai"))
                        .toInstant());
    }

    public void attachOpsSource(String id, OpsNodeSource source, long version) {
        var duplicate =
                nodes().stream()
                        .filter(n -> !n.nodeId().equals(id) && source.sameInstance(n.opsSource()))
                        .findFirst();
        if (duplicate.isPresent()) throw new PreviewFailure("OPS_INSTANCE_ALREADY_LINKED");
        if (sql.update(
                        "UPDATE preview_node SET ops_controller_id=?,ops_target_id=?,ops_instance_id=?,ops_release_id=?,ops_synced_at=?,version=version+1 WHERE node_id=? AND version=? AND node_kind<>'LOCAL' AND mode<>'REMOTE'",
                        source.controllerId(),
                        source.targetId(),
                        source.instanceId(),
                        source.releaseId(),
                        java.time.LocalDateTime.ofInstant(
                                java.time.Instant.now(), java.time.ZoneId.of("Asia/Shanghai")),
                        id,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public void synchronizeManaged(
            String id,
            PreviewNodeConnection c,
            String configuration,
            OpsNodeSource source,
            long version) {
        register(c, configuration, version);
        attachOpsSource(id, source, version + 1);
    }

    public void synchronizeZlm(
            String id,
            ZlmDirectConnection c,
            String configuration,
            OpsNodeSource source,
            String installPath,
            long version) {
        if (sql.update(
                        "UPDATE preview_node SET name=?,control_uri=?,token_path=?,media_address=?,zlm_install_path=?,config_version=?,version=version+1 WHERE node_id=? AND node_kind='ZLM_DIRECT' AND mode='DRAINING' AND version=?",
                        c.name(),
                        c.apiUri().toString(),
                        c.secretFile().toString(),
                        c.mediaAddress(),
                        installPath,
                        configuration,
                        id,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        attachOpsSource(id, source, version + 1);
    }

    public void localConfiguration(String configuration, long version) {
        if (sql.update(
                        "UPDATE preview_node SET config_version=?,version=version+1 WHERE node_id='local' AND version=?",
                        configuration,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public void policy(String id, String mode, int priority, PreviewBudget b, long version) {
        if (sql.update(
                        "UPDATE preview_node SET mode=?,priority=?,max_sources=?,max_viewers=?,max_transcodes=?,max_starting=?,ingress_kbps=?,egress_kbps=?,source_upper_kbps=?,output_upper_kbps=?,version=version+1 WHERE node_id=? AND version=?",
                        mode,
                        priority,
                        b.maxSources(),
                        b.maxViewers(),
                        b.maxTranscodes(),
                        b.maxStarting(),
                        b.ingressKbps(),
                        b.egressKbps(),
                        b.sourceUpperKbps(),
                        b.outputUpperKbps(),
                        id,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public void registerZlm(
            ZlmDirectConnection connection,
            PreviewBudget budget,
            int priority,
            String configVersion) {
        registerZlm(connection, budget, priority, configVersion, null);
    }

    public void registerZlm(
            ZlmDirectConnection connection,
            PreviewBudget budget,
            int priority,
            String configVersion,
            String installPath) {
        if (nodes().size() >= 17) throw new PreviewFailure("NODE_REGISTRY_FULL");
        sql.update(
                "INSERT INTO preview_node(id,node_id,name,control_uri,token_path,media_address,zlm_install_path,node_kind,mode,config_version,priority,max_sources,max_viewers,max_transcodes,max_starting,ingress_kbps,egress_kbps,source_upper_kbps,output_upper_kbps,version) VALUES(?,?,?,?,?,?,?,'ZLM_DIRECT','LOCAL',?,?,?,?,?,?,?,?,?,?,0)",
                sql.queryForObject("SELECT COALESCE(MAX(id),0)+1 FROM preview_node", Long.class),
                connection.nodeId(),
                connection.name(),
                connection.apiUri().toString(),
                connection.secretFile().toString(),
                connection.mediaAddress(),
                installPath,
                configVersion,
                priority,
                budget.maxSources(),
                budget.maxViewers(),
                budget.maxTranscodes(),
                budget.maxStarting(),
                budget.ingressKbps(),
                budget.egressKbps(),
                budget.sourceUpperKbps(),
                budget.outputUpperKbps());
    }

    public void zlmPolicy(
            String id,
            String mode,
            int priority,
            PreviewBudget budget,
            String configVersion,
            long version) {
        zlmPolicy(id, mode, priority, budget, configVersion, zlmInstallPath(id), version);
    }

    public void zlmPolicy(
            String id,
            String mode,
            int priority,
            PreviewBudget budget,
            String configVersion,
            String installPath,
            long version) {
        if (sql.update(
                        "UPDATE preview_node SET mode=?,priority=?,max_sources=?,max_viewers=?,max_transcodes=?,max_starting=?,ingress_kbps=?,egress_kbps=?,source_upper_kbps=?,output_upper_kbps=?,config_version=COALESCE(?,config_version),zlm_install_path=?,version=version+1 WHERE node_id=? AND node_kind='ZLM_DIRECT' AND version=?",
                        mode,
                        priority,
                        budget.maxSources(),
                        budget.maxViewers(),
                        budget.maxTranscodes(),
                        budget.maxStarting(),
                        budget.ingressKbps(),
                        budget.egressKbps(),
                        budget.sourceUpperKbps(),
                        budget.outputUpperKbps(),
                        configVersion,
                        installPath,
                        id,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public String zlmInstallPath(String id) {
        return sql.queryForObject(
                "SELECT zlm_install_path FROM preview_node WHERE node_id=? AND node_kind='ZLM_DIRECT'",
                String.class,
                id);
    }

    public void zlmCredential(String id, Path file, String configVersion, long version) {
        if (sql.update(
                        "UPDATE preview_node SET token_path=?,config_version=?,version=version+1 WHERE node_id=? AND node_kind='ZLM_DIRECT' AND mode<>'REMOTE' AND version=?",
                        file.toString(),
                        configVersion,
                        id,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public void deleteZlm(String id, long version) {
        if (sql.update(
                        "DELETE FROM preview_node WHERE node_id=? AND node_kind='ZLM_DIRECT' AND mode<>'REMOTE' AND version=?",
                        id,
                        version)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public boolean hasOwnership(String id) {
        return sql.queryForObject(
                        "SELECT (SELECT COUNT(*) FROM preview_source WHERE node_id=?) + (SELECT COUNT(*) FROM preview_viewer WHERE node_id=?) + (SELECT COUNT(*) FROM preview_operation WHERE node_id=? AND state IN ('PENDING','UNKNOWN'))",
                        Long.class,
                        id,
                        id,
                        id)
                > 0;
    }

    public void register(PreviewNodeConnection c, String configVersion, Long expected) {
        if (expected == null) {
            if (nodes().size() >= 17) throw new PreviewFailure("NODE_REGISTRY_FULL");
            sql.update(
                    "INSERT INTO preview_node(id,node_id,name,control_uri,certificate_sha256,token_path,media_address,mode,config_version,version) VALUES(?,?,?,?,?,?,?,'LOCAL',?,0)",
                    sql.queryForObject(
                            "SELECT COALESCE(MAX(id),0)+1 FROM preview_node", Long.class),
                    c.nodeId(),
                    c.name(),
                    c.controlUri().toString(),
                    c.certificateSha256(),
                    c.tokenFile().toString(),
                    c.mediaAddress(),
                    configVersion);
        } else if (sql.update(
                        "UPDATE preview_node SET node_id=?,name=?,control_uri=?,certificate_sha256=?,token_path=?,media_address=?,config_version=?,version=version+1 WHERE node_id=? AND version=? AND mode IN ('LOCAL','DRAINING')",
                        c.nodeId(),
                        c.name(),
                        c.controlUri().toString(),
                        c.certificateSha256(),
                        c.tokenFile().toString(),
                        c.mediaAddress(),
                        configVersion,
                        c.nodeId(),
                        expected)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public void mode(String mode, long expected) {
        if (sql.update(
                        "UPDATE preview_node SET mode=?,version=version+1 WHERE id=1 AND version=?",
                        mode,
                        expected)
                != 1) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
    }

    public List<Viewer> viewers() {
        return sql.query(
                "SELECT * FROM preview_viewer ORDER BY viewer_id LIMIT 1089",
                (row, i) -> viewer(row));
    }

    public Viewer viewer(String id) {
        return sql
                .query(
                        "SELECT * FROM preview_viewer WHERE viewer_id=?",
                        (row, i) -> viewer(row),
                        id)
                .stream()
                .findFirst()
                .orElse(null);
    }

    private Viewer viewer(ResultSet row) throws SQLException {
        return new Viewer(
                row.getString("viewer_id"),
                row.getString("node_id"),
                row.getString("boot_id"),
                row.getString("config_version"),
                row.getString("state"));
    }

    public void prepare(Viewer viewer) {
        sql.update(
                "INSERT INTO preview_viewer(viewer_id,node_id,boot_id,config_version,state) VALUES(?,?,?,?,?)",
                viewer.id(),
                viewer.nodeId(),
                viewer.bootId(),
                viewer.configVersion(),
                viewer.state());
    }

    public boolean canReuseSource(String id) {
        return sql.queryForObject(
                        "SELECT COUNT(*) FROM preview_viewer a JOIN preview_viewer b ON a.profile_id=b.profile_id AND a.connection_key=b.connection_key AND a.node_id=b.node_id WHERE a.viewer_id=? AND b.viewer_id<>a.viewer_id AND b.state='READY'",
                        Integer.class,
                        id)
                > 0;
    }

    public boolean transcodeAllowed(String id) {
        return sql.queryForObject(
                        "SELECT COUNT(*) FROM preview_viewer v JOIN preview_output o ON v.profile_id=o.profile_id AND v.output_key=o.output_key WHERE viewer_id=? AND o.transcode_units>0",
                        Integer.class,
                        id)
                > 0;
    }

    public boolean scheduled(String id) {
        return sql.queryForObject(
                        "SELECT COUNT(*) FROM preview_viewer WHERE viewer_id=? AND output_key IS NOT NULL",
                        Integer.class,
                        id)
                > 0;
    }

    public void source(String id, long profile, String connectionKey) {
        if (sql.queryForObject(
                        "SELECT COUNT(*) FROM preview_viewer WHERE profile_id=? AND connection_key<>?",
                        Integer.class,
                        profile,
                        connectionKey)
                > 0) throw new PreviewFailure("SOURCE_GENERATION_PENDING");
        sql.update(
                "UPDATE preview_viewer SET profile_id=?,connection_key=? WHERE viewer_id=?",
                profile,
                connectionKey,
                id);
    }

    public void state(String id, String state) {
        sql.update("UPDATE preview_viewer SET state=? WHERE viewer_id=?", state, id);
    }

    public void closed(String id) {
        sql.update("DELETE FROM preview_viewer WHERE viewer_id=?", id);
    }

    public List<Viewer> viewers(String nodeId) {
        return viewers().stream().filter(v -> v.nodeId().equals(nodeId)).toList();
    }

    public void operation(Command command, String digest) {
        // Terminal history is bounded in time and count; unresolved operations keep their viewer.
        sql.update(
                "DELETE FROM preview_operation WHERE deadline_at<? AND state IN ('SUCCEEDED','FAILED','CLOSED')",
                System.currentTimeMillis() - 120000);
        if (sql.queryForObject("SELECT COUNT(*) FROM preview_operation", Integer.class) >= 32768)
            throw new PreviewFailure("NODE_JOURNAL_FULL");
        sql.update(
                "INSERT INTO preview_operation(operation_id,node_id,viewer_id,action,command_digest,deadline_at,state) VALUES(?,?,?,?,?,?,'PENDING')",
                command.operationId(),
                command.nodeId(),
                command.resourceId(),
                command.action().name(),
                digest,
                command.deadline());
    }

    public void receipt(Receipt receipt) {
        sql.update(
                "UPDATE preview_operation SET state=?,reason_code=? WHERE operation_id=?",
                receipt.state(),
                receipt.reasonCode(),
                receipt.operationId());
    }

    public void resolved(String id) {
        sql.update(
                "UPDATE preview_operation SET state='CLOSED',reason_code='RESOURCE_CLOSED' WHERE viewer_id=? AND state IN ('PENDING','UNKNOWN')",
                id);
    }
}
