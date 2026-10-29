package com.streamfusion.platform.inspection.evidence;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.mapper.CameraScopeMapper;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.storage.StorageConfigurationService;
import com.streamfusion.platform.storage.s3.StorageHttpTransport;
import com.streamfusion.platform.storage.s3.StorageS3Client;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.pojo.enums.UserStatus;
import com.streamfusion.platform.system.user.service.UserService;
import java.util.Base64;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.http.ContentDisposition;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Service;
import org.springframework.web.servlet.mvc.method.annotation.StreamingResponseBody;

/**
 * Browser evidence reads recheck camera visibility on every request and never expose storage
 * credentials.
 */
@Service
@RequiredArgsConstructor
public class InspectionEvidenceReadService {
    private final InspectionEvidenceMapper evidence;
    private final CameraAccessService cameraAccess;
    private final CameraScopeMapper cameraScopes;
    private final CurrentUserService current;
    private final UserService users;
    private final AccessService access;
    private final StorageConfigurationService storage;
    private final StorageS3Client s3;

    public Map<String, Object> detail(String evidenceId) {
        var actor = cameraAccess.readActor();
        var row = committed(evidenceId);
        cameraAccess.requireVisible(row.getCameraId(), actor);
        var result = new LinkedHashMap<String, Object>();
        result.put("evidenceId", row.getEvidenceId());
        result.put("runId", row.getRunId());
        result.put("assignmentEpoch", row.getAssignmentEpoch());
        result.put("cameraId", row.getCameraId().toString());
        result.put("profileId", row.getProfileId().toString());
        result.put("kind", row.getKind());
        result.put("sizeBytes", row.getSizeBytes());
        result.put("contentType", row.getContentType());
        result.put("sha256", row.getSha256());
        result.put(
                "checksumBase64",
                Base64.getEncoder().encodeToString(HexFormat.of().parseHex(row.getSha256())));
        result.put("committedAt", row.getCommittedAt().toInstant(java.time.ZoneOffset.UTC));
        result.put("objectVersionId", row.getObjectVersionId());
        return result;
    }

    public ResponseEntity<StreamingResponseBody> content(
            String evidenceId, String disposition, String range) {
        if (!Set.of("inline", "attachment").contains(disposition))
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        if (range != null
                && (!range.matches("bytes=(?:[0-9]+-[0-9]*|-[0-9]+)") || range.length() > 80))
            throw BusinessException.error(ErrorCode.STORAGE_RANGE_INVALID);
        var actor = cameraAccess.readActor();
        var row = committed(evidenceId);
        cameraAccess.requireVisible(row.getCameraId(), actor);
        var instance = storage.require(row.getInstanceId());
        var object = s3.head(instance, row.getBucket(), row.getObjectKey());
        if (object.size() != row.getSizeBytes()
                || !row.getContentType().equals(object.contentType())
                || !row.getEtag().equals(object.etag())
                || row.getObjectVersionId() != null
                        && !row.getObjectVersionId().equals(object.objectVersionId()))
            throw BusinessException.error(ErrorCode.STORAGE_OBJECT_CHANGED);
        StorageHttpTransport.Response remote = s3.content(instance, row.getBucket(), object, range);
        if ((range != null && !validRangeResponse(remote, row.getSizeBytes(), range))
                || (range == null
                        && (remote.status() != 200
                                || remote.headers().containsKey("content-range")
                                || !Long.toString(row.getSizeBytes())
                                        .equals(remote.headers().get("content-length"))))) {
            remote.close();
            throw BusinessException.error(ErrorCode.STORAGE_RANGE_INVALID);
        }
        try {
            String type = row.getContentType().toLowerCase(Locale.ROOT);
            boolean inline =
                    disposition.equals("inline")
                            && Set.of("image/jpeg", "image/png", "video/mp4", "video/x-matroska")
                                    .contains(type);
            var headers = new HttpHeaders();
            headers.setContentType(
                    MediaType.parseMediaType(inline ? type : "application/octet-stream"));
            String suffix =
                    switch (row.getContentType()) {
                        case "image/png" -> ".png";
                        case "image/jpeg" -> ".jpg";
                        case "video/mp4" -> ".mp4";
                        default -> ".mkv";
                    };
            headers.setContentDisposition(
                    ContentDisposition.builder(inline ? "inline" : "attachment")
                            .filename(row.getEvidenceId() + suffix)
                            .build());
            headers.setCacheControl("private, no-store");
            headers.set("X-Content-Type-Options", "nosniff");
            headers.set("Accept-Ranges", "bytes");
            if (remote.headers().containsKey("content-length")) {
                long length = Long.parseLong(remote.headers().get("content-length"));
                if (length < 0 || length > row.getSizeBytes()) throw new NumberFormatException();
                headers.setContentLength(length);
            }
            if (remote.headers().containsKey("content-range"))
                headers.set("Content-Range", remote.headers().get("content-range"));
            long expectedBytes =
                    range == null
                            ? row.getSizeBytes()
                            : Long.parseLong(remote.headers().get("content-length"));
            StreamingResponseBody body =
                    output -> {
                        try (remote) {
                            byte[] buffer = new byte[65536];
                            long nextCheck = 0;
                            long streamed = 0;
                            for (int length; (length = remote.body().read(buffer)) != -1; ) {
                                streamed += length;
                                if (streamed > row.getSizeBytes())
                                    throw new java.io.IOException("Evidence exceeds indexed size");
                                if (System.nanoTime() >= nextCheck) {
                                    if (!stillVisible(
                                            actor.userId(), row.getCameraId(), row.getId()))
                                        throw new java.io.IOException("Evidence access changed");
                                    nextCheck =
                                            System.nanoTime()
                                                    + java.util.concurrent.TimeUnit.SECONDS.toNanos(
                                                            2);
                                }
                                output.write(buffer, 0, length);
                            }
                            if (streamed != expectedBytes)
                                throw new java.io.IOException(
                                        "Evidence length differs from response");
                        }
                    };
            return new ResponseEntity<>(
                    body,
                    headers,
                    org.springframework.http.HttpStatusCode.valueOf(remote.status()));
        } catch (RuntimeException ex) {
            remote.close();
            if (ex instanceof NumberFormatException)
                throw BusinessException.error(ErrorCode.STORAGE_OBJECT_CHANGED);
            throw ex;
        }
    }

    private static boolean validRangeResponse(
            StorageHttpTransport.Response response, long totalSize, String requested) {
        if (response.status() != 206) return false;
        String header = response.headers().get("content-range");
        if (header == null) return false;
        var matched =
                java.util.regex.Pattern.compile("bytes ([0-9]+)-([0-9]+)/([0-9]+)").matcher(header);
        if (!matched.matches()) return false;
        try {
            long start = Long.parseLong(matched.group(1));
            long end = Long.parseLong(matched.group(2));
            long total = Long.parseLong(matched.group(3));
            String[] parts = requested.substring(6).split("-", -1);
            long wantedStart;
            long wantedEnd;
            if (parts[0].isEmpty()) {
                long suffix = Long.parseLong(parts[1]);
                if (suffix < 1) return false;
                wantedStart = Math.max(0, totalSize - suffix);
                wantedEnd = totalSize - 1;
            } else {
                wantedStart = Long.parseLong(parts[0]);
                wantedEnd =
                        parts[1].isEmpty()
                                ? totalSize - 1
                                : Math.min(totalSize - 1, Long.parseLong(parts[1]));
            }
            String length = response.headers().get("content-length");
            String contentLength = response.headers().get("content-length");
            if (contentLength == null) return false;
            return total == totalSize
                    && start == wantedStart
                    && end == wantedEnd
                    && start <= end
                    && Long.parseLong(contentLength) == end - start + 1;
        } catch (NumberFormatException ex) {
            return false;
        }
    }

    private boolean stillVisible(long userId, long cameraId, long evidenceRowId) {
        var user = users.getById(userId);
        var row = evidence.selectById(evidenceRowId);
        return user != null
                && user.getStatus() == UserStatus.NORMAL.getCode()
                && !CurrentUserService.requiresPasswordChange(user)
                && current.hasModuleAccess(userId, "camera")
                && row != null
                && "COMMITTED".equals(row.getState())
                && cameraScopes.visibleCamera(
                                cameraId,
                                cameraAccess.visibility(
                                        new CameraAccessService.Actor(
                                                userId, access.isSuperAdmin(userId))))
                        == 1;
    }

    private InspectionEvidence committed(String evidenceId) {
        if (evidenceId == null || !evidenceId.matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,63}"))
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        var row =
                evidence.selectOne(
                        new LambdaQueryWrapper<InspectionEvidence>()
                                .eq(InspectionEvidence::getEvidenceId, evidenceId)
                                .eq(InspectionEvidence::getState, "COMMITTED")
                                .orderByDesc(InspectionEvidence::getPlacementRevision)
                                .last("LIMIT 1"));
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }
}
