package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.mapper.CameraCreateRequestMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraCreateRequestEntity;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.Map;
import java.util.Set;
import java.util.function.Consumer;
import java.util.function.Supplier;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Propagation;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.web.context.request.RequestContextHolder;
import org.springframework.web.context.request.ServletRequestAttributes;

/**
 * Durable receipts for exactly three synchronous camera create operations. Caller owns the lock.
 */
@Service
@RequiredArgsConstructor
public class CameraCreateService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraCreateRequestMapper receipts;
    private final CameraCryptoService crypto;
    private final CurrentUserService currentUser;
    private final ObjectMapper json;
    private final Clock clock;

    @Transactional(propagation = Propagation.MANDATORY)
    public Map<String, Object> execute(
            String operation,
            Long parentId,
            String clientRequestId,
            Object canonicalRequest,
            Supplier<Map<String, Object>> create,
            Consumer<Map<String, Object>> recheckReplay) {
        if (!Set.of("CREATE_SOURCE", "CREATE_CAMERA", "ADD_PROFILE").contains(operation))
            throw CameraSourceRules.invalid();
        Instant requested = CameraRequestKey.timestamp(clientRequestId);
        long actorId = currentUser.requireNormal().getId();
        if (!(RequestContextHolder.getRequestAttributes()
                        instanceof ServletRequestAttributes servlet)
                || servlet.getRequest().getSession(false) == null)
            throw BusinessException.error(ErrorCode.UNAUTHORIZED);
        String sessionId = servlet.getRequest().getSession(false).getId();
        Instant now = clock.instant();
        var payload = new java.util.LinkedHashMap<String, Object>();
        payload.put("operation", operation);
        payload.put("parentId", parentId);
        payload.put("command", canonicalRequest);
        // Search by user/key first: old-key digests must continue to match across key rotation.
        var candidates =
                receipts.selectList(
                        new LambdaQueryWrapper<CameraCreateRequestEntity>()
                                .eq(CameraCreateRequestEntity::getActorUserId, actorId)
                                .eq(
                                        CameraCreateRequestEntity::getClientRequestId,
                                        clientRequestId));
        for (var receipt : candidates) {
            byte[] digest = crypto.digest(receipt.getSessionDigestKeyId(), "session", sessionId);
            if (!MessageDigest.isEqual(digest, receipt.getActorSessionDigest())) continue;
            if (!now.isBefore(receipt.getExpiresAt().atZone(ZONE).toInstant()))
                throw BusinessException.error(ErrorCode.CONFLICT);
            if (!operation.equals(receipt.getOperation())
                    || !java.util.Objects.equals(parentId, receipt.getParentResourceId())
                    || !MessageDigest.isEqual(
                            crypto.digest(receipt.getFingerprintKeyId(), "create", payload),
                            receipt.getRequestFingerprint()))
                throw BusinessException.error(ErrorCode.CONFLICT);
            Map<String, Object> result;
            try {
                result = json.readValue(receipt.getResultSummary(), new TypeReference<>() {});
            } catch (Exception ex) {
                throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
            }
            recheckReplay.accept(result);
            return result;
        }
        CameraRequestKey.requireFresh(requested, now);
        long recent =
                receipts.selectCount(
                        new LambdaQueryWrapper<CameraCreateRequestEntity>()
                                .eq(CameraCreateRequestEntity::getActorUserId, actorId)
                                .ge(
                                        CameraCreateRequestEntity::getCreatedAt,
                                        LocalDateTime.ofInstant(now.minusSeconds(60), ZONE)));
        if (recent >= 30) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        String keyId = crypto.activeKeyId();
        // Resolve keys before creating resources; failures still roll back with the caller.
        byte[] sessionDigest = crypto.digest(keyId, "session", sessionId);
        byte[] fingerprint = crypto.digest(keyId, "create", payload);
        Map<String, Object> result = create.get();
        String summary = crypto.canonical(result);
        if (summary.getBytes(java.nio.charset.StandardCharsets.UTF_8).length > 32768)
            throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        String resourceKey =
                switch (operation) {
                    case "CREATE_SOURCE" -> "sourceId";
                    case "CREATE_CAMERA" -> "cameraId";
                    default -> "streamProfileId";
                };
        var receipt = new CameraCreateRequestEntity();
        receipt.setActorUserId(actorId);
        receipt.setActorSessionDigest(sessionDigest);
        receipt.setSessionDigestKeyId(keyId);
        receipt.setClientRequestId(clientRequestId);
        receipt.setOperation(operation);
        receipt.setParentResourceId(parentId);
        receipt.setRequestFingerprint(fingerprint);
        receipt.setFingerprintKeyId(keyId);
        receipt.setResourceId(Long.valueOf(result.get(resourceKey).toString()));
        receipt.setResultSummary(summary);
        receipt.setRequestedAt(LocalDateTime.ofInstant(requested, ZONE));
        receipt.setCreatedAt(LocalDateTime.ofInstant(now, ZONE));
        receipt.setExpiresAt(LocalDateTime.ofInstant(now.plusSeconds(86400), ZONE));
        receipts.insert(receipt);
        var expired =
                receipts.selectList(
                        new LambdaQueryWrapper<CameraCreateRequestEntity>()
                                .select(CameraCreateRequestEntity::getId)
                                .lt(CameraCreateRequestEntity::getExpiresAt, receipt.getCreatedAt())
                                .orderByAsc(CameraCreateRequestEntity::getExpiresAt)
                                .last("LIMIT 100"));
        if (!expired.isEmpty())
            receipts.deleteByIds(expired.stream().map(CameraCreateRequestEntity::getId).toList());
        return result;
    }
}
