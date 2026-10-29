package com.streamfusion.platform.inspection.evidence;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.ArgumentMatchers.isNull;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import com.streamfusion.platform.camera.mapper.CameraScopeMapper;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.storage.StorageConfigurationService;
import com.streamfusion.platform.storage.entity.StorageInstance;
import com.streamfusion.platform.storage.s3.StorageHttpTransport;
import com.streamfusion.platform.storage.s3.StorageS3Client;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.pojo.entity.UserEntity;
import com.streamfusion.platform.system.user.service.UserService;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.Socket;
import java.time.Instant;
import java.time.LocalDateTime;
import java.util.Map;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.Semaphore;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;

@ExtendWith(MockitoExtension.class)
class InspectionEvidenceReadTest {
    @Mock InspectionEvidenceMapper evidence;
    @Mock CameraAccessService cameras;
    @Mock CameraScopeMapper cameraScopes;
    @Mock CurrentUserService current;
    @Mock UserService users;
    @Mock AccessService access;
    @Mock StorageConfigurationService storage;
    @Mock StorageS3Client s3;
    InspectionEvidenceReadService reads;
    InspectionEvidence row;

    @BeforeEach
    void setup() {
        reads =
                new InspectionEvidenceReadService(
                        evidence, cameras, cameraScopes, current, users, access, storage, s3);
        row = new InspectionEvidence();
        row.setId(17L);
        row.setEvidenceId("evidence-a");
        row.setRunId("run-a");
        row.setAssignmentEpoch(1);
        row.setPlacementRevision(1);
        row.setCameraId(23L);
        row.setProfileId(24L);
        row.setInstanceId(30L);
        row.setBucket("evidence-bucket");
        row.setObjectKey("evidence/run-a/evidence-a/1.jpg");
        row.setKind("IMAGE");
        row.setContentType("image/jpeg");
        row.setSizeBytes(10L);
        row.setSha256("0".repeat(64));
        row.setState("COMMITTED");
        row.setEtag("\"etag\"");
        row.setCommittedAt(LocalDateTime.now());
        when(cameras.readActor()).thenReturn(new CameraAccessService.Actor(1L, true));
    }

    @Test
    void detailChecksCurrentCameraPermissionAndDoesNotExposeStorageLocation() {
        when(evidence.selectOne(any())).thenReturn(row);
        var detail = reads.detail("evidence-a");
        assertThat(detail).containsEntry("cameraId", "23");
        assertThat(detail).doesNotContainKeys("bucket", "objectKey", "putUrl", "instanceId");
        verify(cameras).requireVisible(eq(23L), any());
        doThrow(BusinessException.error(ErrorCode.NOT_FOUND))
                .when(cameras)
                .requireVisible(eq(23L), any());
        assertThatThrownBy(() -> reads.detail("evidence-a"))
                .isInstanceOf(BusinessException.class)
                .extracting(error -> ((BusinessException) error).code())
                .isEqualTo(ErrorCode.NOT_FOUND);
    }

    @Test
    void unknownEvidenceCannotBeRead() {
        assertThatThrownBy(() -> reads.detail("evidence-a"))
                .isInstanceOf(BusinessException.class)
                .extracting(error -> ((BusinessException) error).code())
                .isEqualTo(ErrorCode.NOT_FOUND);
    }

    @Test
    void malformedOrMismatchedRangeClosesRemoteResponse() {
        when(evidence.selectOne(any())).thenReturn(row);
        var instance = new StorageInstance();
        instance.setId(30L);
        when(storage.require(30L)).thenReturn(instance);
        var object =
                new StorageS3Client.ObjectInfo(
                        row.getObjectKey(), 10, "\"etag\"", Instant.now(), "image/jpeg", null);
        when(s3.head(any(), anyString(), anyString())).thenReturn(object);
        var permits = new Semaphore(0);
        when(s3.content(any(), anyString(), any(), eq("bytes=0-4")))
                .thenReturn(
                        remote(
                                206,
                                Map.of("content-range", "bytes 1-5/10", "content-length", "5"),
                                permits));
        assertThatThrownBy(() -> reads.content("evidence-a", "inline", "bytes=0-4"))
                .isInstanceOf(BusinessException.class)
                .extracting(error -> ((BusinessException) error).code())
                .isEqualTo(ErrorCode.STORAGE_RANGE_INVALID);
        assertThat(permits.availablePermits()).isOne();
        assertThatThrownBy(() -> reads.content("evidence-a", "inline", "bytes=0-4,6-7"))
                .isInstanceOf(BusinessException.class)
                .extracting(error -> ((BusinessException) error).code())
                .isEqualTo(ErrorCode.STORAGE_RANGE_INVALID);
    }

    @Test
    void partialResponseWithoutRangeIsRejectedAndClosed() {
        when(evidence.selectOne(any())).thenReturn(row);
        var instance = new StorageInstance();
        instance.setId(30L);
        when(storage.require(30L)).thenReturn(instance);
        when(s3.head(any(), anyString(), anyString()))
                .thenReturn(
                        new StorageS3Client.ObjectInfo(
                                row.getObjectKey(),
                                10,
                                "\"etag\"",
                                Instant.now(),
                                "image/jpeg",
                                null));
        var permits = new Semaphore(0);
        when(s3.content(any(), anyString(), any(), isNull()))
                .thenReturn(
                        remote(
                                206,
                                Map.of("content-range", "bytes 0-4/10", "content-length", "5"),
                                permits));
        assertThatThrownBy(() -> reads.content("evidence-a", "inline", null))
                .isInstanceOf(BusinessException.class)
                .extracting(error -> ((BusinessException) error).code())
                .isEqualTo(ErrorCode.STORAGE_RANGE_INVALID);
        assertThat(permits.availablePermits()).isOne();
    }

    @Test
    void fullResponseRequiresIndexedLengthAndRangeRequiresExactInterval() {
        when(evidence.selectOne(any())).thenReturn(row);
        var instance = new StorageInstance();
        instance.setId(30L);
        when(storage.require(30L)).thenReturn(instance);
        when(s3.head(any(), anyString(), anyString()))
                .thenReturn(
                        new StorageS3Client.ObjectInfo(
                                row.getObjectKey(),
                                10,
                                "\"etag\"",
                                Instant.now(),
                                "image/jpeg",
                                null));
        var badPermits = new Semaphore(0);
        when(s3.content(any(), anyString(), any(), isNull()))
                .thenReturn(remote(200, Map.of("content-length", "9"), badPermits));
        assertThatThrownBy(() -> reads.content("evidence-a", "inline", null))
                .isInstanceOf(BusinessException.class);
        assertThat(badPermits.availablePermits()).isOne();

        var goodPermits = new Semaphore(0);
        when(s3.content(any(), anyString(), any(), eq("bytes=0-4")))
                .thenReturn(
                        remote(
                                206,
                                Map.of("content-range", "bytes 0-4/10", "content-length", "5"),
                                goodPermits));
        var response = reads.content("evidence-a", "inline", "bytes=0-4");
        assertThat(response.getStatusCode().value()).isEqualTo(206);
        assertThat(response.getHeaders().getFirst("Content-Range")).isEqualTo("bytes 0-4/10");
        assertThat(response.getHeaders().getFirst("X-Content-Type-Options")).isEqualTo("nosniff");
        assertThat(goodPermits.availablePermits()).isZero();
    }

    @Test
    void revokedCameraStopsStreamBeforeFirstByte() {
        when(evidence.selectOne(any())).thenReturn(row);
        var instance = new StorageInstance();
        instance.setId(30L);
        when(storage.require(30L)).thenReturn(instance);
        when(s3.head(any(), anyString(), anyString()))
                .thenReturn(
                        new StorageS3Client.ObjectInfo(
                                row.getObjectKey(),
                                10,
                                "\"etag\"",
                                Instant.now(),
                                "image/jpeg",
                                null));
        when(s3.content(any(), anyString(), any(), isNull()))
                .thenReturn(
                        new StorageHttpTransport.Response(
                                200,
                                Map.of("content-length", "10"),
                                new ByteArrayInputStream(new byte[10]),
                                new Socket(),
                                new Socket(),
                                org.mockito.Mockito.mock(ScheduledFuture.class),
                                new Semaphore(0),
                                new AtomicBoolean()));
        var user = new UserEntity();
        user.setStatus(1);
        user.setMustChangePassword(false);
        when(users.getById(1L)).thenReturn(user);
        when(current.hasModuleAccess(1L, "camera")).thenReturn(true);
        when(evidence.selectById(17L)).thenReturn(row);
        when(cameraScopes.visibleCamera(eq(23L), any())).thenReturn(0L);
        var response = reads.content("evidence-a", "inline", null);
        assertThatThrownBy(() -> response.getBody().writeTo(new ByteArrayOutputStream()))
                .isInstanceOf(IOException.class)
                .hasMessageContaining("access changed");
    }

    private static StorageHttpTransport.Response remote(
            int status, Map<String, String> headers, Semaphore permits) {
        return new StorageHttpTransport.Response(
                status,
                headers,
                new ByteArrayInputStream(new byte[0]),
                new Socket(),
                new Socket(),
                org.mockito.Mockito.mock(ScheduledFuture.class),
                permits,
                new AtomicBoolean());
    }
}
