package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraAssetRules.invalid;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.camera.mapper.CameraAssetRow;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraDeviceMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.dto.CameraCreateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraInitialProfileDto;
import com.streamfusion.platform.camera.pojo.dto.CameraLocatorDto;
import com.streamfusion.platform.camera.pojo.dto.CameraProfileCreateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraProfileUpdateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraQueryDto;
import com.streamfusion.platform.camera.pojo.dto.CameraUpdateDto;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraDeviceEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceGroupVo;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceVo;
import com.streamfusion.platform.camera.pojo.vo.CameraProfileVo;
import com.streamfusion.platform.camera.pojo.vo.CameraVo;
import com.streamfusion.platform.camera.service.CameraAccessService.Actor;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.CameraReferenceDetails;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.common.validation.VersionCounter;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

/** Local assets only. Saving these records never discovers devices or starts media. */
@Service
@RequiredArgsConstructor
public class CameraAssetService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private static final Set<String> LIFECYCLES =
            Set.of("PENDING_ASSIGNMENT", "ENABLED", "DISABLED");
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraDeviceMapper devices;
    private final CameraAccessService access;
    private final CameraSourceService sources;
    private final CameraLocatorService locators;
    private final CameraCreateService creates;
    private final CameraGroupService groups;
    private final AuditService audit;
    private final Clock clock;

    public Map<String, Boolean> options() {
        boolean admin = access.readActor().superAdmin();
        return Map.of(
                "canManageShared", admin, "manualStorageReady", admin && sources.storageReady());
    }

    @Transactional(readOnly = true)
    public PageResultVo<CameraVo> page(CameraQueryDto query) {
        if (query == null) throw invalid("query");
        Actor actor = access.readActor();
        var filter = queryFilter(query);
        var page = channels.pageVisible(query.toPage(), filter, access.visibility(actor));
        Map<Long, String> paths =
                groups.paths(
                        page.getRecords().stream()
                                .map(CameraChannelEntity::getGroupId)
                                .filter(Objects::nonNull)
                                .distinct()
                                .toList());
        return PageResultVo.from(
                page,
                page.getRecords().stream()
                        .map(row -> view(row, actor, paths.get(row.getGroupId()), null))
                        .toList());
    }

    private CameraChannelMapper.Filter queryFilter(CameraQueryDto query) {
        if (query == null) throw invalid("query");
        String name = CameraAssetRules.text(query.getName(), 128, "name", true);
        if (name != null) name = name.replace("!", "!!").replace("%", "!%").replace("_", "!_");
        Long sourceId = optionalId(query.getSourceId(), "sourceId");
        Long groupId = optionalId(query.getGroupId(), "groupId");
        if (query.getLifecycle() != null && !LIFECYCLES.contains(query.getLifecycle()))
            throw invalid("lifecycle");
        if (query.getAdapterType() != null && !sources.supportsAdapter(query.getAdapterType()))
            throw invalid("adapterType");
        return new CameraChannelMapper.Filter(
                name,
                sourceId,
                query.getAdapterType(),
                query.getLifecycle(),
                groupId == null
                        ? null
                        : groups.queryGroupIds(groupId, query.isIncludeDescendants()));
    }

    @Transactional(readOnly = true)
    public PageResultVo<CameraDeviceGroupVo> deviceGroups(CameraQueryDto query) {
        Actor actor = access.readActor();
        var filter = queryFilter(query);
        var page = channels.pageDeviceGroups(query.toPage(), filter, access.visibility(actor));
        return PageResultVo.from(
                page,
                page.getRecords().stream()
                        .map(
                                row ->
                                        new CameraDeviceGroupVo(
                                                row.getGroupKey(),
                                                row.getName(),
                                                row.isIdentified(),
                                                row.getManufacturer(),
                                                row.getModel(),
                                                row.getSourceDisplayName(),
                                                row.getSourceType(),
                                                row.getConnectionCategory(),
                                                row.getChannelCount(),
                                                row.getEnabledCount(),
                                                row.getDisabledCount(),
                                                row.getPendingCount()))
                        .toList());
    }

    @Transactional(readOnly = true)
    public PageResultVo<CameraVo> deviceChannels(String groupKey, CameraQueryDto query) {
        if (groupKey == null || !groupKey.matches("[dc][1-9][0-9]{0,18}"))
            throw invalid("groupKey");
        DecimalInput.id(groupKey.substring(1), "groupKey");
        Actor actor = access.readActor();
        var filter = queryFilter(query);
        var page =
                channels.pageDeviceChannels(
                        query.toPage(), groupKey, filter, access.visibility(actor));
        Map<Long, String> paths =
                groups.paths(
                        page.getRecords().stream()
                                .map(CameraChannelEntity::getGroupId)
                                .filter(Objects::nonNull)
                                .distinct()
                                .toList());
        return PageResultVo.from(
                page,
                page.getRecords().stream()
                        .map(row -> view(row, actor, paths.get(row.getGroupId()), null))
                        .toList());
    }

    @Transactional(readOnly = true)
    public CameraVo detail(String cameraId) {
        return detail(DecimalInput.id(cameraId, "cameraId"), access.readActor());
    }

    @Transactional(readOnly = true)
    public List<CameraProfileVo> profiles(String cameraId) {
        Actor actor = access.readActor();
        long id = DecimalInput.id(cameraId, "cameraId");
        access.requireVisible(id, actor);
        return profileEntities(id).stream().map(profile -> profileView(profile, actor)).toList();
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> create(CameraCreateDto input, AuditContextDto context) {
        if (input == null) throw invalid("camera");
        Actor actor = access.lockActor();
        access.requireSuper(actor);
        boolean inline = input.getConnection() != null;
        if (inline && (input.getSourceId() != null || input.getSourceVersion() != null))
            throw invalid("connection");
        Long sourceId = inline ? null : DecimalInput.id(input.getSourceId(), "sourceId");
        Long sourceVersion =
                inline ? null : DecimalInput.version(input.getSourceVersion(), "sourceVersion");
        input.setName(CameraAssetRules.text(input.getName(), 128, "name", false));
        input.setRemark(CameraAssetRules.text(input.getRemark(), 500, "remark", true));
        validateInitialProfiles(input);
        if (inline && !input.getProfiles().isEmpty()) throw invalid("profiles");
        return creates.execute(
                "CREATE_CAMERA",
                sourceId,
                input.getClientRequestId(),
                input,
                () -> {
                    var source =
                            inline
                                    ? sources.createManualDevice(
                                            input.getConnection(), input.getName(), actor, context)
                                    : sources.requireArchiveSource(sourceId, sourceVersion);
                    return createChannel(
                            source.getId(), source.getVersion(), input, actor, context);
                },
                result -> access.requireVisible(resultId(result, "cameraId"), actor));
    }

    private Map<String, Object> createChannel(
            long sourceId,
            long sourceVersion,
            CameraCreateDto input,
            Actor actor,
            AuditContextDto context) {
        var source = sources.requireArchiveSource(sourceId, sourceVersion);
        if (!input.getProfiles().isEmpty()) sources.requireManualSource(sourceId, sourceVersion);
        LocalDateTime now = now();
        CameraChannelEntity camera = new CameraChannelEntity();
        camera.setSourceId(sourceId);
        camera.setExternalChannelKey("manual:" + UUID.randomUUID());
        camera.setName(input.getName());
        camera.setRemark(input.getRemark());
        camera.setLifecycle("PENDING_ASSIGNMENT");
        camera.setMappingOrigin("MANUAL");
        camera.setVersion(0L);
        camera.setCreatedAt(now);
        camera.setUpdatedAt(now);
        camera.setCreatedBy(actor.userId());
        camera.setUpdatedBy(actor.userId());
        requireWrite(channels.insert(camera));
        List<Map<String, Object>> created = new ArrayList<>();
        Long defaultProfileId = null;
        for (CameraInitialProfileDto item : input.getProfiles()) {
            CameraProfileEntity profile =
                    insertProfile(
                            camera,
                            item.getLabel(),
                            item.getUsageHint(),
                            item.getEnabled(),
                            item.getLocator(),
                            actor);
            created.add(
                    Map.of(
                            "clientKey",
                            item.getClientKey(),
                            "streamProfileId",
                            profile.getId().toString(),
                            "version",
                            "0"));
            if (Objects.equals(item.getClientKey(), input.getDefaultProfileClientKey()))
                defaultProfileId = profile.getId();
        }
        if (defaultProfileId != null) {
            // The first value is part of creation, so the externally returned version remains zero.
            requireWrite(
                    channels.update(
                            null,
                            new LambdaUpdateWrapper<CameraChannelEntity>()
                                    .eq(CameraChannelEntity::getId, camera.getId())
                                    .set(
                                            CameraChannelEntity::getDefaultPreviewProfileId,
                                            defaultProfileId)));
        }
        record(actor, "CAMERA", camera.getId(), "CAMERA_CREATE", camera.getName(), context);
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("cameraId", camera.getId().toString());
        result.put("sourceId", source.getId().toString());
        result.put("sourceVersion", source.getVersion().toString());
        result.put("version", "0");
        result.put("lifecycle", camera.getLifecycle());
        result.put("defaultPreviewProfileId", string(defaultProfileId));
        result.put("profiles", created);
        return result;
    }

    private void validateInitialProfiles(CameraCreateDto input) {
        if (input.getProfiles() == null) input.setProfiles(List.of());
        if (input.getProfiles().size() > 8) throw invalid("profiles");
        Set<String> keys = new HashSet<>();
        boolean defaultFound = input.getDefaultProfileClientKey() == null;
        for (CameraInitialProfileDto profile : input.getProfiles()) {
            if (profile == null) throw invalid("profiles");
            profile.setClientKey(
                    CameraAssetRules.text(profile.getClientKey(), 64, "clientKey", false));
            if (!keys.add(profile.getClientKey())) throw invalid("clientKey");
            profile.setLabel(CameraAssetRules.text(profile.getLabel(), 64, "label", false));
            CameraAssetRules.usage(profile.getUsageHint());
            CameraAssetRules.enabled(profile.getEnabled());
            CameraAssetRules.rtsp(profile.getLocatorKind());
            if (profile.getLocator() == null) throw invalid("locator");
            if (Objects.equals(profile.getClientKey(), input.getDefaultProfileClientKey())) {
                if (!profile.getEnabled()) throw invalid("defaultProfileClientKey");
                defaultFound = true;
            }
        }
        if (!defaultFound) throw invalid("defaultProfileClientKey");
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraVo update(String cameraId, CameraUpdateDto input, AuditContextDto context) {
        if (input == null) throw invalid("camera");
        Actor actor = access.lockActor();
        long id = DecimalInput.id(cameraId, "cameraId");
        CameraChannelEntity camera = lockedCamera(id, actor);
        long version = DecimalInput.version(input.getVersion(), "version");
        requireVersion(camera.getVersion(), version, false);
        if (!actor.superAdmin()
                && (input.isGroupIdProvided()
                        || input.isLifecycleProvided()
                        || input.isConfirmationProvided())) {
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        }
        String name =
                input.isNameProvided()
                        ? CameraAssetRules.text(input.getName(), 128, "name", false)
                        : camera.getName();
        String remark =
                input.isRemarkProvided()
                        ? CameraAssetRules.text(input.getRemark(), 500, "remark", true)
                        : camera.getRemark();
        Long defaultId =
                input.isDefaultPreviewProfileIdProvided()
                        ? optionalId(input.getDefaultPreviewProfileId(), "defaultPreviewProfileId")
                        : camera.getDefaultPreviewProfileId();
        Long groupId =
                input.isGroupIdProvided()
                        ? optionalId(input.getGroupId(), "groupId")
                        : camera.getGroupId();
        String lifecycle =
                input.isLifecycleProvided() ? input.getLifecycle() : camera.getLifecycle();
        if (input.isLifecycleProvided() && (lifecycle == null || !LIFECYCLES.contains(lifecycle)))
            throw invalid("lifecycle");
        if (input.isDefaultPreviewProfileIdProvided() && defaultId != null)
            validateDefault(id, defaultId, null);
        if (!Objects.equals(groupId, camera.getGroupId())
                || !Objects.equals(lifecycle, camera.getLifecycle())) {
            groups.validateCameraChange(
                    actor,
                    new CameraGroupService.CameraPlacement(
                            id, version, camera.getGroupId(), camera.getLifecycle()),
                    groupId,
                    lifecycle,
                    input.getConfirmation());
        }
        if (Objects.equals(name, camera.getName())
                && Objects.equals(remark, camera.getRemark())
                && Objects.equals(defaultId, camera.getDefaultPreviewProfileId())
                && Objects.equals(groupId, camera.getGroupId())
                && Objects.equals(lifecycle, camera.getLifecycle())) return detail(id, actor);
        VersionCounter.requireIncrementable(version);
        requireVersionWrite(
                channels.update(
                        null,
                        new LambdaUpdateWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getId, id)
                                .eq(CameraChannelEntity::getVersion, version)
                                .set(CameraChannelEntity::getName, name)
                                .set(CameraChannelEntity::getRemark, remark)
                                .set(CameraChannelEntity::getDefaultPreviewProfileId, defaultId)
                                .set(CameraChannelEntity::getGroupId, groupId)
                                .set(CameraChannelEntity::getLifecycle, lifecycle)
                                .setIncrBy(CameraChannelEntity::getVersion, 1)
                                .set(CameraChannelEntity::getUpdatedAt, now())
                                .set(CameraChannelEntity::getUpdatedBy, actor.userId())));
        recordCameraUpdate(
                actor, camera, name, groupId, lifecycle, defaultId, version + 1, context);
        return detail(id, actor);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String cameraId, String requestedVersion, AuditContextDto context) {
        Actor actor = access.lockActor();
        access.requireSuper(actor);
        long id = DecimalInput.id(cameraId, "cameraId");
        CameraChannelEntity camera = lockedCamera(id, actor);
        requireVersion(
                camera.getVersion(), DecimalInput.version(requestedVersion, "version"), true);
        long grantCount = channels.directGrantCount(id);
        if (grantCount > 0) throw referenced("DIRECT_CAMERA_GRANT", grantCount);
        // Profiles and locators are owned children. Explicit cleanup preserves all external FKs.
        if (camera.getDefaultPreviewProfileId() != null)
            requireWrite(
                    channels.update(
                            null,
                            new LambdaUpdateWrapper<CameraChannelEntity>()
                                    .eq(CameraChannelEntity::getId, id)
                                    .set(CameraChannelEntity::getDefaultPreviewProfileId, null)));
        for (CameraProfileEntity profile : profileEntities(id)) {
            locators.delete(profile.getId());
            requireWrite(profiles.deleteById(profile.getId()));
        }
        requireWrite(channels.deleteById(id));
        record(actor, "CAMERA", id, "CAMERA_DELETE", camera.getName(), context);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> addProfile(
            String cameraId, CameraProfileCreateDto input, AuditContextDto context) {
        if (input == null) throw invalid("profile");
        Actor actor = access.lockActor();
        access.requireSuper(actor);
        long id = DecimalInput.id(cameraId, "cameraId");
        long version = DecimalInput.version(input.getCameraVersion(), "cameraVersion");
        input.setLabel(CameraAssetRules.text(input.getLabel(), 64, "label", false));
        CameraAssetRules.usage(input.getUsageHint());
        CameraAssetRules.enabled(input.getEnabled());
        CameraAssetRules.rtsp(input.getLocatorKind());
        if (input.getLocator() == null) throw invalid("locator");
        return creates.execute(
                "ADD_PROFILE",
                id,
                input.getClientRequestId(),
                input,
                () -> {
                    CameraChannelEntity camera = lockedCamera(id, actor);
                    requireVersion(camera.getVersion(), version, false);
                    requireManual(camera);
                    if (profiles.selectCount(
                                    new LambdaQueryWrapper<CameraProfileEntity>()
                                            .eq(CameraProfileEntity::getChannelId, id))
                            >= 32) throw invalid("profiles");
                    CameraProfileEntity profile =
                            insertProfile(
                                    camera,
                                    input.getLabel(),
                                    input.getUsageHint(),
                                    input.getEnabled(),
                                    input.getLocator(),
                                    actor);
                    record(
                            actor,
                            "CAMERA_PROFILE",
                            profile.getId(),
                            "CAMERA_PROFILE_CREATE",
                            profile.getLabel(),
                            context);
                    return Map.of(
                            "streamProfileId",
                            profile.getId().toString(),
                            "version",
                            "0",
                            "cameraId",
                            cameraId);
                },
                result -> {
                    access.requireVisible(id, actor);
                    requireProfile(id, resultId(result, "streamProfileId"), false);
                });
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraProfileVo updateProfile(
            String cameraId,
            String profileId,
            CameraProfileUpdateDto input,
            AuditContextDto context) {
        if (input == null) throw invalid("profile");
        Actor actor = access.lockActor();
        access.requireSuper(actor);
        long id = DecimalInput.id(cameraId, "cameraId");
        long pid = DecimalInput.id(profileId, "streamProfileId");
        CameraChannelEntity camera = lockedCamera(id, actor);
        CameraProfileEntity profile = requireProfile(id, pid, true);
        long version = DecimalInput.version(input.getVersion(), "version");
        requireVersion(profile.getVersion(), version, false);
        String label =
                input.isLabelProvided()
                        ? CameraAssetRules.text(input.getLabel(), 64, "label", false)
                        : profile.getLabel();
        String usage =
                input.isUsageHintProvided()
                        ? CameraAssetRules.usage(input.getUsageHint())
                        : profile.getUsageHint();
        boolean enabled =
                input.isEnabledProvided()
                        ? CameraAssetRules.enabled(input.getEnabled())
                        : profile.getEnabled();
        if (input.isLocatorKindProvided()) {
            requireManual(camera);
            CameraAssetRules.rtsp(input.getLocatorKind());
        }
        if (input.isLocatorProvided()) {
            if (input.getLocator() == null) throw invalid("locator");
            requireManual(camera);
        }
        boolean clearingDefault =
                !enabled && Objects.equals(camera.getDefaultPreviewProfileId(), pid);
        if (clearingDefault) {
            if (!input.isReplacementDefaultProfileIdProvided())
                throw invalid("replacementDefaultProfileId");
            long cameraVersion = DecimalInput.version(input.getCameraVersion(), "cameraVersion");
            requireVersion(camera.getVersion(), cameraVersion, false);
            Long replacement =
                    optionalId(
                            input.getReplacementDefaultProfileId(), "replacementDefaultProfileId");
            if (replacement != null) validateDefault(id, replacement, pid);
            changeDefault(camera, replacement, actor, context);
        } else if (input.isReplacementDefaultProfileIdProvided()
                || input.getCameraVersion() != null) {
            throw invalid("replacementDefaultProfileId");
        }
        String usageOrigin = input.isUsageHintProvided() ? "MANUAL" : profile.getUsageOrigin();
        boolean changed =
                !Objects.equals(label, profile.getLabel())
                        || !Objects.equals(usage, profile.getUsageHint())
                        || !Objects.equals(usageOrigin, profile.getUsageOrigin())
                        || enabled != profile.getEnabled()
                        || input.isLocatorProvided();
        if (!changed) return profileView(profile, actor);
        VersionCounter.requireIncrementable(version);
        if (input.isLocatorProvided())
            locators.update(
                    camera.getSourceId(), pid, profile.getExternalProfileKey(), input.getLocator());
        requireVersionWrite(
                profiles.update(
                        null,
                        new LambdaUpdateWrapper<CameraProfileEntity>()
                                .eq(CameraProfileEntity::getId, pid)
                                .eq(CameraProfileEntity::getVersion, version)
                                .set(CameraProfileEntity::getLabel, label)
                                .set(CameraProfileEntity::getUsageHint, usage)
                                .set(CameraProfileEntity::getUsageOrigin, usageOrigin)
                                .set(CameraProfileEntity::getEnabled, enabled)
                                .setIncrBy(CameraProfileEntity::getVersion, 1)
                                .set(CameraProfileEntity::getUpdatedAt, now())
                                .set(CameraProfileEntity::getUpdatedBy, actor.userId())));
        audit.record(
                actor.userId(),
                "CAMERA_PROFILE",
                pid,
                "CAMERA_PROFILE_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "name",
                        label,
                        "label",
                        label,
                        "usageHint",
                        usage,
                        "enabled",
                        Boolean.toString(enabled)));
        return profileView(requireProfile(id, pid, false), actor);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void deleteProfile(
            String cameraId, String profileId, String requestedVersion, AuditContextDto context) {
        Actor actor = access.lockActor();
        access.requireSuper(actor);
        long id = DecimalInput.id(cameraId, "cameraId");
        long pid = DecimalInput.id(profileId, "streamProfileId");
        CameraChannelEntity camera = lockedCamera(id, actor);
        CameraProfileEntity profile = requireProfile(id, pid, true);
        requireVersion(
                profile.getVersion(), DecimalInput.version(requestedVersion, "version"), true);
        if (Objects.equals(camera.getDefaultPreviewProfileId(), pid))
            throw referenced("DEFAULT_PREVIEW_PROFILE", 1);
        locators.delete(pid);
        requireWrite(profiles.deleteById(pid));
        record(actor, "CAMERA_PROFILE", pid, "CAMERA_PROFILE_DELETE", profile.getLabel(), context);
    }

    @Transactional(readOnly = true)
    public CameraDeviceVo device(String deviceId) {
        Actor actor = access.readActor();
        access.requireSuper(actor);
        return deviceView(requireDevice(DecimalInput.id(deviceId, "deviceId"), false));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void deleteDevice(String deviceId, String requestedVersion, AuditContextDto context) {
        Actor actor = access.lockActor();
        access.requireSuper(actor);
        long id = DecimalInput.id(deviceId, "deviceId");
        CameraDeviceEntity device = requireDevice(id, true);
        requireVersion(
                device.getVersion(), DecimalInput.version(requestedVersion, "version"), true);
        long count =
                channels.selectCount(
                        new LambdaQueryWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getDeviceId, id));
        if (count > 0) throw referenced("CAMERA_CHANNEL", count);
        requireWrite(devices.deleteById(id));
        record(actor, "CAMERA_DEVICE", id, "CAMERA_DEVICE_DELETE", device.getSourceName(), context);
    }

    private CameraProfileEntity insertProfile(
            CameraChannelEntity camera,
            String label,
            String usage,
            boolean enabled,
            CameraLocatorDto locator,
            Actor actor) {
        CameraProfileEntity profile = new CameraProfileEntity();
        profile.setSourceId(camera.getSourceId());
        profile.setChannelId(camera.getId());
        profile.setExternalProfileKey("manual:" + UUID.randomUUID());
        profile.setLabel(label);
        profile.setUsageHint(usage);
        profile.setUsageOrigin("UNKNOWN".equals(usage) ? "UNKNOWN" : "MANUAL");
        profile.setEnabled(enabled);
        profile.setVersion(0L);
        profile.setCreatedAt(now());
        profile.setUpdatedAt(profile.getCreatedAt());
        profile.setCreatedBy(actor.userId());
        profile.setUpdatedBy(actor.userId());
        requireWrite(profiles.insert(profile));
        locators.create(
                camera.getSourceId(), profile.getId(), profile.getExternalProfileKey(), locator);
        return profile;
    }

    private void requireManual(CameraChannelEntity camera) {
        if (!"MANUAL".equals(camera.getMappingOrigin()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        sources.requireManualSource(camera.getSourceId(), null);
    }

    private void changeDefault(
            CameraChannelEntity camera, Long replacement, Actor actor, AuditContextDto context) {
        VersionCounter.requireIncrementable(camera.getVersion());
        requireVersionWrite(
                channels.update(
                        null,
                        new LambdaUpdateWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getId, camera.getId())
                                .eq(CameraChannelEntity::getVersion, camera.getVersion())
                                .set(CameraChannelEntity::getDefaultPreviewProfileId, replacement)
                                .setIncrBy(CameraChannelEntity::getVersion, 1)
                                .set(CameraChannelEntity::getUpdatedAt, now())
                                .set(CameraChannelEntity::getUpdatedBy, actor.userId())));
        recordCameraUpdate(
                actor,
                camera,
                camera.getName(),
                camera.getGroupId(),
                camera.getLifecycle(),
                replacement,
                camera.getVersion() + 1,
                context);
    }

    private void validateDefault(long cameraId, long profileId, Long excludedId) {
        CameraProfileEntity selected = requireProfile(cameraId, profileId, false);
        if (Objects.equals(excludedId, profileId)
                || !selected.getEnabled()
                || !locators.valid(profileId)) throw invalid("defaultPreviewProfileId");
    }

    private CameraChannelEntity lockedCamera(long id, Actor actor) {
        access.requireVisible(id, actor);
        CameraChannelEntity value =
                channels.selectOne(
                        new LambdaQueryWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getId, id)
                                .last("FOR UPDATE"));
        if (value == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return value;
    }

    private CameraProfileEntity requireProfile(long cameraId, long profileId, boolean lock) {
        var query =
                new LambdaQueryWrapper<CameraProfileEntity>()
                        .eq(CameraProfileEntity::getId, profileId)
                        .eq(CameraProfileEntity::getChannelId, cameraId);
        if (lock) query.last("FOR UPDATE");
        CameraProfileEntity profile = profiles.selectOne(query);
        if (profile == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return profile;
    }

    private CameraDeviceEntity requireDevice(long id, boolean lock) {
        var query = new LambdaQueryWrapper<CameraDeviceEntity>().eq(CameraDeviceEntity::getId, id);
        if (lock) query.last("FOR UPDATE");
        CameraDeviceEntity device = devices.selectOne(query);
        if (device == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return device;
    }

    private List<CameraProfileEntity> profileEntities(long cameraId) {
        return profiles.selectList(
                new LambdaQueryWrapper<CameraProfileEntity>()
                        .eq(CameraProfileEntity::getChannelId, cameraId)
                        .orderByAsc(CameraProfileEntity::getId));
    }

    private CameraVo detail(long id, Actor actor) {
        CameraAssetRow row = channels.visible(id, access.visibility(actor));
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        String path =
                row.getGroupId() == null
                        ? null
                        : groups.paths(List.of(row.getGroupId())).get(row.getGroupId());
        return view(
                row,
                actor,
                path,
                profileEntities(id).stream().map(profile -> profileView(profile, actor)).toList());
    }

    private static CameraVo view(
            CameraAssetRow row, Actor actor, String path, List<CameraProfileVo> items) {
        CameraVo.DeviceSummary device =
                row.getDeviceId() == null
                        ? null
                        : new CameraVo.DeviceSummary(
                                row.getDeviceType(), row.getManufacturer(), row.getModel());
        return new CameraVo(
                row.getId().toString(),
                row.getName(),
                row.getRemark(),
                row.getSourceName(),
                row.getSourceDisplayName(),
                string(row.getGroupId()),
                path,
                row.getSourceType(),
                row.getConnectionCategory(),
                row.getVendorHint(),
                device,
                row.getLifecycle(),
                string(row.getDefaultPreviewProfileId()),
                row.getVersion().toString(),
                instant(row.getCreatedAt()),
                instant(row.getUpdatedAt()),
                actor.superAdmin() ? row.getSourceId().toString() : null,
                actor.superAdmin() ? string(row.getSourceVersion()) : null,
                actor.superAdmin() ? string(row.getDeviceId()) : null,
                actor.superAdmin() ? row.getExternalChannelKey() : null,
                items);
    }

    private CameraProfileVo profileView(CameraProfileEntity value, Actor actor) {
        return new CameraProfileVo(
                value.getId().toString(),
                value.getLabel(),
                value.getUsageHint(),
                value.getUsageOrigin(),
                value.getEnabled(),
                value.getVideoCodec(),
                value.getWidth(),
                value.getHeight(),
                value.getFrameRate(),
                value.getBitrateKbps(),
                value.getAudioCodec(),
                value.getHasAudio(),
                instant(value.getParametersObservedAt()),
                value.getParametersOrigin(),
                value.getVersion().toString(),
                locators.summary(value.getId(), actor.superAdmin()),
                CameraStreamClassification.classify(
                        value.getUsageHint(), value.getUsageOrigin(), value.getSourceLabel()));
    }

    private static CameraDeviceVo deviceView(CameraDeviceEntity value) {
        return new CameraDeviceVo(
                value.getId().toString(),
                value.getSourceId().toString(),
                value.getExternalDeviceKey(),
                value.getExternalDeviceRef(),
                value.getDeviceType(),
                value.getSourceName(),
                value.getManufacturer(),
                value.getModel(),
                value.getSerialNumber(),
                value.getFirmwareVersion(),
                instant(value.getInfoObservedAt()),
                value.getVersion().toString(),
                instant(value.getCreatedAt()),
                instant(value.getUpdatedAt()));
    }

    private void record(
            Actor actor,
            String type,
            long id,
            String action,
            String name,
            AuditContextDto context) {
        Map<String, Object> summary = new LinkedHashMap<>();
        summary.put("name", name);
        audit.record(actor.userId(), type, id, action, "SUCCESS", null, context, summary);
    }

    private void recordCameraUpdate(
            Actor actor,
            CameraChannelEntity before,
            String name,
            Long groupId,
            String lifecycle,
            Long defaultId,
            long nextVersion,
            AuditContextDto context) {
        Map<String, Object> summary = new LinkedHashMap<>();
        summary.put("name", name);
        summary.put("beforeGroupId", string(before.getGroupId()));
        summary.put("afterGroupId", string(groupId));
        summary.put("beforeLifecycle", before.getLifecycle());
        summary.put("afterLifecycle", lifecycle);
        summary.put("beforeDefaultProfileId", string(before.getDefaultPreviewProfileId()));
        summary.put("afterDefaultProfileId", string(defaultId));
        summary.put("beforeVersion", before.getVersion().toString());
        summary.put("afterVersion", Long.toString(nextVersion));
        audit.record(
                actor.userId(),
                "CAMERA",
                before.getId(),
                "CAMERA_UPDATE",
                "SUCCESS",
                null,
                context,
                summary);
    }

    private static BusinessException referenced(String type, long count) {
        return BusinessException.error(
                ErrorCode.CAMERA_REFERENCED,
                new CameraReferenceDetails(
                        List.of(new CameraReferenceDetails.Reference(type, count))));
    }

    private static void requireVersion(long actual, long expected, boolean delete) {
        if (actual != expected)
            throw BusinessException.error(
                    delete ? ErrorCode.PRECONDITION_FAILED : ErrorCode.VERSION_CONFLICT);
    }

    private static void requireVersionWrite(int count) {
        if (count != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
    }

    private static void requireWrite(int count) {
        if (count != 1) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
    }

    private static long resultId(Map<String, Object> result, String field) {
        return DecimalInput.id((String) result.get(field), field);
    }

    private static Long optionalId(String value, String field) {
        return value == null ? null : DecimalInput.id(value, field);
    }

    private static String string(Long value) {
        return value == null ? null : value.toString();
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(BUSINESS_ZONE).toInstant();
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), BUSINESS_ZONE);
    }
}
