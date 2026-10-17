package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;

class ZlmDirectExecutorTest {
    @TempDir Path directory;
    private static final String VIEWER = "12345678-1234-1234-1234-123456789abc";
    private static final String NODE = "zlm-1234567890abcdef";

    @Test
    void expiredUnknownOpenBecomesClosedOnlyAfterExactCleanup() {
        var fixture = fixture(System.currentTimeMillis() - 1);
        fixture.executor.close(VIEWER, PreviewDeadline.after(Duration.ofSeconds(3)));
        assertThat(
                        fixture.sql.queryForObject(
                                "SELECT state FROM preview_operation WHERE operation_id='aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'",
                                String.class))
                .isEqualTo("CLOSED");
        verify(fixture.gateway, times(2)).close(any(), any(), any());
    }

    @Test
    void uncertainOpenRetainsOwnershipDuringLateCreateFence() {
        var fixture = fixture(System.currentTimeMillis() + 30000);
        assertThatThrownBy(
                        () ->
                                fixture.executor.close(
                                        VIEWER, PreviewDeadline.after(Duration.ofSeconds(3))))
                .isInstanceOf(PreviewFailure.class)
                .hasMessageContaining("CLEANUP_PENDING");
        assertThat(
                        fixture.sql.queryForObject(
                                "SELECT state FROM preview_operation WHERE operation_id='aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'",
                                String.class))
                .isEqualTo("PENDING");
    }

    private Fixture fixture(long notBefore) {
        var data = new JdbcDataSource();
        data.setURL("jdbc:h2:mem:" + java.util.UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        var sql = new JdbcTemplate(data);
        sql.execute(
                "CREATE TABLE preview_viewer (viewer_id VARCHAR(36),node_id VARCHAR(64),profile_id BIGINT,connection_key VARCHAR(64))");
        sql.execute(
                "CREATE TABLE preview_operation (operation_id VARCHAR(36),node_id VARCHAR(64),viewer_id VARCHAR(36),action VARCHAR(16),command_digest VARCHAR(64),deadline_at BIGINT,state VARCHAR(16),reason_code VARCHAR(80))");
        sql.update("INSERT INTO preview_viewer VALUES(?,?,?,?)", VIEWER, NODE, 7, "conn");
        var repository = mock(PreviewNodeRepository.class);
        var gateway = mock(ZlmDirectGateway.class);
        var connection =
                new ZlmDirectConnection(
                        NODE,
                        URI.create("http://127.0.0.1:8080"),
                        directory.resolve("secret"),
                        "127.0.0.1");
        var node =
                new PreviewNodeRepository.Node(
                        null, "REMOTE", "cfg", 0, PreviewBudget.defaults(64, 0), 100, connection);
        when(repository.viewer(VIEWER))
                .thenReturn(new PreviewNodeRepository.Viewer(VIEWER, NODE, "boot", "cfg", "READY"));
        when(repository.node(NODE)).thenReturn(node);
        // The source tuple is deterministic but private. Mock the query result for source
        // journal rows by inserting through the same hash formula.
        String app = "sf_preview_" + digest(NODE).substring(0, 12);
        String stream = digest("7\nconn\ncfg").substring(0, 32);
        String key = NODE + ":" + app + ":" + stream;
        sql.update(
                "INSERT INTO preview_operation VALUES(?,?,?,?,?,?,?,?)",
                "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
                NODE,
                VIEWER,
                "CREATE",
                digest(key),
                notBefore,
                "PENDING",
                null);
        return new Fixture(sql, gateway, new ZlmDirectExecutor(repository, gateway, sql));
    }

    private static String digest(String value) {
        try {
            return java.util.HexFormat.of()
                    .formatHex(
                            java.security.MessageDigest.getInstance("SHA-256")
                                    .digest(
                                            value.getBytes(
                                                    java.nio.charset.StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException impossible) {
            throw new IllegalStateException(impossible);
        }
    }

    private record Fixture(
            JdbcTemplate sql, ZlmDirectGateway gateway, ZlmDirectExecutor executor) {}
}
