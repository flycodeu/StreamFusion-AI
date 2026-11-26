package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraAssetRules.invalid;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraDeviceMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.mapper.CameraScopeMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraDeviceEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraSourceEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceDeleteVo;
import com.streamfusion.platform.camera.service.CameraAccessService.Actor;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.CameraReferenceDetails;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class CameraAssetRemovalService {
    private final CameraChannelMapper channels;
    private final CameraDeviceMapper devices;
    private final CameraProfileMapper profiles;
    private final CameraScopeMapper scopes;
    private final CameraAccessService access;
    private final CameraAssetRelations relations;
    private final CameraLocatorService locators;
    private final CameraSourceService sources;
    private final CameraSourceQueries sourceQueries;
    private final AuditService audit;

    @Transactional(readOnly = true, isolation = Isolation.REPEATABLE_READ)
    public CameraDeviceDeleteVo previewDeviceDelete(String groupKey) {
        access.requireSuper(access.readActor());
        var snapshot = snapshot(groupKey, false);
        return new CameraDeviceDeleteVo(
                groupKey,
                snapshot.channels().size(),
                snapshot.removesSource(),
                snapshot.revision());
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public boolean deleteDeviceAsset(String groupKey, String header, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        if (header == null) throw BusinessException.error(ErrorCode.PRECONDITION_REQUIRED);
        if (!header.matches("\"[rk]-[a-f0-9]{64}\"")) throw invalid("revision");

        var snapshot = snapshot(groupKey, true);
        boolean removeSource = header.charAt(1) == 'r';
        if (!header.substring(3, header.length() - 1).equals(snapshot.fingerprint())
                || (removeSource && !snapshot.removesSource()))
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        for (var camera : snapshot.channels()) {
            clearDefault(camera);
        }
        for (var profile : snapshot.profiles()) {
            locators.delete(profile.getId());
            requireWrite(profiles.deleteById(profile.getId()));
        }
        for (var camera : snapshot.channels()) {
            requireWrite(channels.deleteById(camera.getId()));
            record(actor, "CAMERA", camera.getId(), "CAMERA_DELETE", camera.getName(), context);
        }
        if (snapshot.device() != null) {
            var device = snapshot.device();
            requireWrite(devices.deleteById(device.getId()));
            record(
                    actor,
                    "CAMERA_DEVICE",
                    device.getId(),
                    "CAMERA_DEVICE_DELETE",
                    device.getSourceName(),
                    context);
        }
        if (removeSource) {
            sources.deleteSource(snapshot.source(), actor, context);
        }
        return removeSource;
    }

    void deleteChannel(CameraChannelEntity camera, Actor actor, AuditContextDto context) {
        long id = camera.getId();
        long grantCount = channels.directGrantCount(id);
        if (grantCount > 0) throw referenced("DIRECT_CAMERA_GRANT", grantCount);
        if (camera.getDeviceId() != null
                && channels.selectCount(
                                new LambdaQueryWrapper<CameraChannelEntity>()
                                        .eq(CameraChannelEntity::getDeviceId, camera.getDeviceId()))
                        == 1) {
            long deviceGrants = scopes.deviceGrantCount(camera.getDeviceId());
            if (deviceGrants > 0) throw referenced("DIRECT_DEVICE_GRANT", deviceGrants);
        }
        clearDefault(camera);
        for (var profile : relations.profiles(id)) {
            locators.delete(profile.getId());
            requireWrite(profiles.deleteById(profile.getId()));
        }
        requireWrite(channels.deleteById(id));
        record(actor, "CAMERA", id, "CAMERA_DELETE", camera.getName(), context);
    }

    private Snapshot snapshot(String groupKey, boolean lock) {
        if (groupKey == null || !groupKey.matches("[dc][1-9][0-9]{0,18}"))
            throw invalid("groupKey");
        long key = DecimalInput.id(groupKey.substring(1), "groupKey");
        var device = groupKey.charAt(0) == 'd' ? relations.device(key, lock) : null;
        var query = new LambdaQueryWrapper<CameraChannelEntity>();
        if (device != null) query.eq(CameraChannelEntity::getDeviceId, key);
        else query.eq(CameraChannelEntity::getId, key).isNull(CameraChannelEntity::getDeviceId);
        var rows =
                channels.selectList(
                        query.orderByAsc(CameraChannelEntity::getId)
                                .last(lock ? "LIMIT 10001 FOR UPDATE" : "LIMIT 10001"));
        CameraGroupRules.bounded(rows.size());
        if (rows.isEmpty()) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (device != null) {
            long grants = scopes.deviceGrantCount(device.getId());
            if (grants > 0) throw referenced("DIRECT_DEVICE_GRANT", grants);
        }
        long channelGrants =
                device == null
                        ? channels.directGrantCount(key)
                        : channels.directDeviceChannelGrantCount(key);
        if (channelGrants > 0) throw referenced("DIRECT_CAMERA_GRANT", channelGrants);

        long sourceId = rows.getFirst().getSourceId();
        var source =
                lock ? sources.lockForAssetDelete(sourceId) : sourceQueries.requireSource(sourceId);
        // A platform connection is a reusable directory configuration even when it has no
        // remaining imported assets.
        boolean exclusive =
                !"PLATFORM".equals(source.getConnectionCategory())
                        && sources.isExclusiveToAsset(sourceId, rows.size(), device != null);
        List<Long> ids = rows.stream().map(CameraChannelEntity::getId).toList();
        List<CameraProfileEntity> members = new ArrayList<>();
        for (int offset = 0; offset < ids.size(); offset += 500) {
            members.addAll(
                    profiles.selectList(
                            new LambdaQueryWrapper<CameraProfileEntity>()
                                    .in(
                                            CameraProfileEntity::getChannelId,
                                            ids.subList(offset, Math.min(offset + 500, ids.size())))
                                    .orderByAsc(CameraProfileEntity::getId)));
        }
        var digest = digest();
        hash(digest, groupKey + ":" + (device == null ? "-" : device.getVersion()));
        hash(digest, source.getId() + ":" + source.getVersion());
        rows.forEach(row -> hash(digest, "c:" + row.getId() + ":" + row.getVersion()));
        members.forEach(row -> hash(digest, "p:" + row.getId() + ":" + row.getVersion()));
        return new Snapshot(
                device,
                rows,
                members,
                source,
                exclusive,
                HexFormat.of().formatHex(digest.digest()));
    }

    private void clearDefault(CameraChannelEntity camera) {
        if (camera.getDefaultPreviewProfileId() != null)
            requireWrite(
                    channels.update(
                            null,
                            new LambdaUpdateWrapper<CameraChannelEntity>()
                                    .eq(CameraChannelEntity::getId, camera.getId())
                                    .set(CameraChannelEntity::getDefaultPreviewProfileId, null)));
    }

    private void record(
            Actor actor,
            String type,
            long id,
            String action,
            String name,
            AuditContextDto context) {
        var summary = new LinkedHashMap<String, Object>();
        summary.put("name", name);
        audit.record(actor.userId(), type, id, action, "SUCCESS", null, context, summary);
    }

    private static MessageDigest digest() {
        try {
            return MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException unavailable) {
            throw new IllegalStateException(unavailable);
        }
    }

    private static void hash(MessageDigest digest, String field) {
        digest.update(field.getBytes(StandardCharsets.US_ASCII));
        digest.update((byte) '\n');
    }

    private static void requireWrite(int count) {
        if (count != 1) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
    }

    private static BusinessException referenced(String type, long count) {
        return BusinessException.error(
                ErrorCode.CAMERA_REFERENCED,
                new CameraReferenceDetails(
                        List.of(new CameraReferenceDetails.Reference(type, count))));
    }

    private record Snapshot(
            CameraDeviceEntity device,
            List<CameraChannelEntity> channels,
            List<CameraProfileEntity> profiles,
            CameraSourceEntity source,
            boolean removesSource,
            String fingerprint) {
        String revision() {
            return (removesSource ? "r-" : "k-") + fingerprint;
        }
    }
}
