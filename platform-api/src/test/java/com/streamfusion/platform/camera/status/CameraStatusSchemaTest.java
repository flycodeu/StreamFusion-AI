package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.support.IdentitySchema;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class CameraStatusSchemaTest {
    private Connection connection;

    @BeforeEach
    void openIsolatedDatabase() throws Exception {
        connection =
                DriverManager.getConnection(
                        "jdbc:h2:mem:camera_status_schema;MODE=MySQL", "sa", "");
        IdentitySchema.initialize(connection);
        execute(
                "INSERT INTO camera_source(id,name,created_at,updated_at) VALUES (1,'Source',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)");
        execute(
                "INSERT INTO camera_channel(id,source_id,external_channel_key,name,mapping_origin) VALUES (2,1,'channel','Camera','MANUAL'),(3,1,'other','Other','MANUAL')");
        execute(
                "INSERT INTO camera_stream_profile(id,source_id,channel_id,external_profile_key,label) VALUES (4,1,2,'main','Main')");
    }

    @AfterEach
    void closeDatabase() throws Exception {
        if (connection != null) connection.close();
    }

    @Test
    void requestKeyIsUniquePerActorAndHistoryDoesNotRequireLiveAssets() throws Exception {
        insertCheck(10, 9, "same-key");
        assertThatThrownBy(() -> insertCheck(11, 9, "same-key")).isInstanceOf(SQLException.class);
        insertCheck(12, 8, "same-key");
        execute("DELETE FROM camera_stream_profile WHERE id=4");
        execute("DELETE FROM camera_channel WHERE id=2");
        assertThat(count("camera_status_check")).isEqualTo(2);
    }

    @Test
    void localBusyCannotMasqueradeAsAnObservationAndEnumsAreRestricted() throws Exception {
        insertCheck(10, 9, "key");
        assertThatThrownBy(
                        () ->
                                execute(
                                        "UPDATE camera_status_check SET result_value='FAILED' WHERE id=10"))
                .isInstanceOf(SQLException.class);
        assertThatThrownBy(
                        () ->
                                execute(
                                        "UPDATE camera_status_check SET execution_state='OFFLINE' WHERE id=10"))
                .isInstanceOf(SQLException.class);
        assertThatThrownBy(
                        () -> execute("UPDATE camera_status_check SET duration_ms=-1 WHERE id=10"))
                .isInstanceOf(SQLException.class);
        execute(
                "UPDATE camera_status_check SET execution_state='SKIPPED',reason_code='WORKER_BUSY',finished_at=CURRENT_TIMESTAMP WHERE id=10");
        assertThat(count("camera_status_snapshot")).isZero();
    }

    @Test
    void snapshotRequiresMatchingChannelAndSurvivesReceiptCleanupButNotProfileDeletion()
            throws Exception {
        insertCheck(10, 9, "key");
        assertThatThrownBy(() -> insertSnapshot(3)).isInstanceOf(SQLException.class);
        insertSnapshot(2);
        execute("DELETE FROM camera_status_check WHERE id=10");
        assertThat(count("camera_status_snapshot")).isEqualTo(1);
        execute("DELETE FROM camera_stream_profile WHERE id=4");
        assertThat(count("camera_status_snapshot")).isZero();
    }

    private void insertCheck(long id, long actorId, String key) throws SQLException {
        try (var sql =
                connection.prepareStatement(
                        "INSERT INTO camera_status_check(id,actor_user_id,camera_id,source_id,profile_id,client_request_id,request_key_id,session_key_id,connection_key_id,request_fingerprint,session_digest,connection_digest,execution_state,trace_id,requested_at,deadline_at,updated_at,retain_until) VALUES (?, ?, 2, 1, 4, ?, 'test', 'test', 'test', ?, ?, ?, 'QUEUED', 'schema-test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)")) {
            sql.setLong(1, id);
            sql.setLong(2, actorId);
            sql.setString(3, key);
            sql.setBytes(4, new byte[32]);
            sql.setBytes(5, new byte[32]);
            sql.setBytes(6, new byte[32]);
            sql.executeUpdate();
        }
    }

    private void insertSnapshot(long cameraId) throws SQLException {
        try (var sql =
                connection.prepareStatement(
                        "INSERT INTO camera_status_snapshot(profile_id,camera_id,observation_value,reason_code,observed_at,read_at,valid_until,check_requested_at,updated_at,check_id,connection_key_id,connection_digest) VALUES (4, ?, 'ACCESSIBLE', 'RTSP_ACCESSIBLE', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 10, 'test', ?)")) {
            sql.setLong(1, cameraId);
            sql.setBytes(2, new byte[32]);
            sql.executeUpdate();
        }
    }

    private void execute(String command) throws SQLException {
        try (var sql = connection.createStatement()) {
            sql.executeUpdate(command);
        }
    }

    private long count(String table) throws SQLException {
        try (var sql = connection.createStatement();
                var rows = sql.executeQuery("SELECT COUNT(*) FROM " + table)) {
            rows.next();
            return rows.getLong(1);
        }
    }
}
