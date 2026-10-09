package com.streamfusion.platform.camera.access.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.access.adapter.CameraAccessCatalog;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.access.pojo.CameraImportResult;
import com.streamfusion.platform.camera.access.pojo.ImportSelection;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraDeviceMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraDeviceEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraAccessService.Actor;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraGroupService;
import com.streamfusion.platform.camera.service.CameraLocatorService;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.camera.service.CameraSourceService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.common.validation.VersionCounter;
import java.math.BigDecimal;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Propagation;
import org.springframework.transaction.annotation.Transactional;

/**
 * Persists a selected catalogue in the caller's locked job transaction; never performs network IO.
 */
@Service
@RequiredArgsConstructor
public class CameraAccessImportService {
    private final CameraSourceService sources;
    private final CameraAccessAdapterRegistry adapters;
    private final CameraLocatorService locators;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraDeviceMapper devices;
    private final CameraGroupService groups;
    private final CameraAccessService access;
    private final CameraCryptoService crypto;
    private final AuditService audit;
    private final Clock clock;

    private record Selected(
            int index,
            CameraAccessCatalog.Channel channel,
            List<Integer> profileIndexes,
            Integer defaultIndex) {}

    private record Prepared(
            List<Selected> selected,
            Long groupId,
            Map<String, CameraChannelEntity> existing,
            Map<Long, List<CameraProfileEntity>> profiles,
            String proof,
            int newCount) {}

    public CameraConnection resolveConnection(CameraConnection input) {
        return sources.resolveConnection(input);
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public Map<String, Object> preview(
            long jobId,
            long version,
            CameraConnection connection,
            CameraAccessCatalog catalog,
            ImportSelection selection,
            Actor actor) {
        var prepared = prepare(connection, catalog, selection, actor);
        var result =
                new LinkedHashMap<>(
                        groups.previewImport(
                                actor,
                                jobId,
                                version,
                                prepared.groupId(),
                                prepared.newCount(),
                                prepared.proof()));
        result.put("cameraCount", prepared.selected().size());
        return result;
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public Map<String, Object> execute(
            long jobId,
            long version,
            CameraConnection connection,
            CameraAccessCatalog catalog,
            ImportSelection selection,
            Actor actor,
            AuditContextDto context) {
        var prepared = prepare(connection, catalog, selection, actor);
        groups.verifyImport(
                actor,
                jobId,
                version,
                prepared.groupId(),
                prepared.proof(),
                selection.confirmation());
        return persist(connection, catalog, selection, actor, context, prepared).toView();
    }

    /**
     * Only the fenced bulk worker calls this, after validating its persisted placement approval.
     */
    @Transactional(propagation = Propagation.MANDATORY)
    public CameraImportResult executeBackground(
            CameraConnection connection,
            CameraAccessCatalog catalog,
            Long groupId,
            Actor actor,
            AuditContextDto context) {
        var selection =
                new ImportSelection(
                        List.of(new ImportSelection.Selection("c0", List.of(), null)),
                        groupId == null ? null : groupId.toString(),
                        null);
        if (catalog.channels().size() != 1
                || !adapters.supportsCategory(catalog.adapterType(), "PLATFORM")) throw invalid();
        return persist(
                connection,
                catalog,
                selection,
                actor,
                context,
                prepare(connection, catalog, selection, actor));
    }

    private CameraImportResult persist(
            CameraConnection connection,
            CameraAccessCatalog catalog,
            ImportSelection selection,
            Actor actor,
            AuditContextDto context,
            Prepared prepared) {
        var source = sources.createImported(connection, catalog.adapterType(), actor, context);
        LocalDateTime observedAt =
                catalog.observedAt() == null
                        ? null
                        : LocalDateTime.ofInstant(catalog.observedAt(), ZoneId.of("Asia/Shanghai"))
                                .truncatedTo(java.time.temporal.ChronoUnit.MICROS);
        Long deviceId = ensureDevice(source.getId(), catalog.device(), actor, observedAt);
        List<CameraImportResult.ImportedCamera> results = new ArrayList<>();
        int created = 0;
        for (var selected : prepared.selected()) {
            var camera = prepared.existing().get(selected.channel().externalKey());
            boolean fresh = camera == null;
            if (fresh) {
                camera = new CameraChannelEntity();
                camera.setSourceId(source.getId());
                camera.setDeviceId(deviceId);
                camera.setExternalChannelKey(selected.channel().externalKey());
                camera.setExternalChannelRef(
                        "RTSP".equals(catalog.adapterType())
                                ? null
                                : selected.channel().externalKey());
                camera.setName(
                        display(selected.channel().name(), 128, "相机 " + (selected.index() + 1)));
                camera.setSourceName(optionalText(selected.channel().name(), 128));
                camera.setGroupId(prepared.groupId());
                camera.setLifecycle(prepared.groupId() == null ? "PENDING_ASSIGNMENT" : "ENABLED");
                camera.setMappingOrigin(
                        "RTSP".equals(catalog.adapterType()) ? "MANUAL" : "ADAPTER");
                camera.setCatalogObservedAt(
                        "RTSP".equals(catalog.adapterType()) ? null : observedAt);
                camera.setVersion(0L);
                camera.setCreatedAt(now());
                camera.setUpdatedAt(camera.getCreatedAt());
                camera.setCreatedBy(actor.userId());
                camera.setUpdatedBy(actor.userId());
                channels.insert(camera);
                created++;
            }
            if (selection.targetCameraId() != null) {
                long previous = camera.getVersion();
                camera.setExternalChannelKey(selected.channel().externalKey());
                camera.setExternalChannelRef(selected.channel().externalKey());
                camera.setDeviceId(deviceId);
                camera.setSourceName(optionalText(selected.channel().name(), 128));
                camera.setMappingOrigin("ADAPTER");
                camera.setCatalogObservedAt(now());
                camera.setUpdatedAt(now());
                camera.setUpdatedBy(actor.userId());
                camera.setVersion(VersionCounter.next(previous));
                if (channels.update(
                                camera,
                                new LambdaUpdateWrapper<CameraChannelEntity>()
                                        .eq(CameraChannelEntity::getId, camera.getId())
                                        .eq(CameraChannelEntity::getVersion, previous))
                        != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
                audit.record(
                        actor.userId(),
                        "CAMERA",
                        camera.getId(),
                        "CAMERA_UPDATE",
                        "SUCCESS",
                        null,
                        context,
                        Map.of(
                                "name",
                                camera.getName(),
                                "beforeVersion",
                                Long.toString(previous),
                                "afterVersion",
                                camera.getVersion().toString()));
            } else if (!fresh
                    && !"RTSP".equals(catalog.adapterType())
                    && newer(observedAt, camera.getCatalogObservedAt())) {
                var update =
                        new LambdaUpdateWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getId, camera.getId())
                                .eq(CameraChannelEntity::getVersion, camera.getVersion())
                                .and(
                                        q ->
                                                q.isNull(CameraChannelEntity::getCatalogObservedAt)
                                                        .or()
                                                        .lt(
                                                                CameraChannelEntity
                                                                        ::getCatalogObservedAt,
                                                                observedAt))
                                .set(
                                        CameraChannelEntity::getSourceName,
                                        observedText(
                                                selected.channel().name(),
                                                128,
                                                camera.getSourceName()))
                                .set(CameraChannelEntity::getCatalogObservedAt, observedAt);
                if (channels.update(null, update) != 1)
                    throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
            }
            Map<String, CameraProfileEntity> known = new HashMap<>();
            for (var profile : prepared.profiles().getOrDefault(camera.getId(), List.of()))
                known.put(profile.getExternalProfileKey(), profile);
            Long defaultId = null;
            for (int index : selected.profileIndexes()) {
                var item = selected.channel().profiles().get(index);
                var profile = known.get(item.externalKey());
                if (profile == null) {
                    profile =
                            insertProfile(
                                    source.getId(),
                                    camera.getId(),
                                    item,
                                    catalog.adapterType(),
                                    actor,
                                    observedAt);
                    locators.createImported(
                            source.getId(),
                            profile.getId(),
                            selected.channel().externalKey(),
                            item.externalKey(),
                            item.locator(),
                            connection.rtspPort() == null ? 554 : connection.rtspPort());
                    audit.record(
                            actor.userId(),
                            "CAMERA_PROFILE",
                            profile.getId(),
                            "CAMERA_PROFILE_CREATE",
                            "SUCCESS",
                            null,
                            context,
                            Map.of("label", profile.getLabel()));
                } else if (!"RTSP".equals(catalog.adapterType())) {
                    refreshProfile(profile, item, observedAt);
                }
                if (Objects.equals(selected.defaultIndex(), index)) defaultId = profile.getId();
            }
            // Repeated discovery never overwrites any local settings of an existing
            // channel/profile.
            if ((fresh || selection.targetCameraId() != null) && defaultId != null)
                channels.update(
                        null,
                        new LambdaUpdateWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getId, camera.getId())
                                .set(CameraChannelEntity::getDefaultPreviewProfileId, defaultId));
            if (fresh) {
                audit.record(
                        actor.userId(),
                        "CAMERA",
                        camera.getId(),
                        "CAMERA_CREATE",
                        "SUCCESS",
                        null,
                        context,
                        Map.of("name", camera.getName()));
            }
            results.add(
                    new CameraImportResult.ImportedCamera(
                            selected.index(),
                            camera.getId(),
                            camera.getVersion(),
                            fresh
                                    ? CameraImportResult.Status.CREATED
                                    : CameraImportResult.Status.EXISTING));
        }
        return new CameraImportResult(
                source.getId(), source.getVersion(), results, created, results.size() - created);
    }

    private Prepared prepare(
            CameraConnection connection,
            CameraAccessCatalog catalog,
            ImportSelection input,
            Actor actor) {
        access.requireSuper(actor);
        if (connection == null
                || catalog == null
                || input == null
                || input.selections() == null
                || input.selections().isEmpty()
                || input.selections().size() > 256
                || !adapters.methods().contains(catalog.adapterType())
                || "AUTO".equals(catalog.adapterType())) throw invalid();
        if (connection.sourceId() != null) {
            var resolved = sources.resolveConnection(connection);
            if (!"AUTO".equals(resolved.method())
                    && !catalog.adapterType().equals(resolved.method())) throw invalid();
        } else if (!"AUTO".equals(connection.method())
                && !catalog.adapterType().equals(connection.method())) throw invalid();
        if ((input.targetCameraId() == null) != (input.targetCameraVersion() == null)
                || input.targetCameraId() != null && connection.sourceId() == null) throw invalid();
        Long group = input.groupId() == null ? null : DecimalInput.id(input.groupId(), "groupId");
        List<Selected> selected = new ArrayList<>();
        Set<Integer> candidates = new HashSet<>();
        Set<String> externalChannels = new HashSet<>();
        for (var command : input.selections()) {
            if (command == null || command.profileIds() == null || command.profileIds().size() > 8)
                throw invalid();
            int index = localIndex(command.candidateId(), 'c', catalog.channels().size());
            if (!candidates.add(index)) throw invalid();
            var channel = catalog.channels().get(index);
            opaque(channel.externalKey(), 512);
            if (channel.mappingRequired() || !externalChannels.add(channel.externalKey()))
                throw invalid();
            Set<Integer> selectedProfiles = new TreeSet<>();
            Set<String> externalProfiles = new HashSet<>();
            for (String profileId : command.profileIds()) {
                int pi = localIndex(profileId, 'p', channel.profiles().size());
                if (!selectedProfiles.add(pi)) throw invalid();
                var profile = channel.profiles().get(pi);
                opaque(profile.externalKey(), 512);
                if (!externalProfiles.add(profile.externalKey())
                        || profile.locator() == null
                        || !catalog.adapterType().equals(profile.locator().kind())) throw invalid();
            }
            Integer defaultIndex =
                    command.defaultProfileId() == null
                            ? null
                            : localIndex(
                                    command.defaultProfileId(), 'p', channel.profiles().size());
            if (defaultIndex != null && !selectedProfiles.contains(defaultIndex)) throw invalid();
            selected.add(new Selected(index, channel, List.copyOf(selectedProfiles), defaultIndex));
        }
        selected.sort(Comparator.comparingInt(Selected::index));
        Map<String, CameraChannelEntity> existing = new TreeMap<>();
        Map<Long, List<CameraProfileEntity>> existingProfiles = new TreeMap<>();
        if (connection.sourceId() != null) {
            long sourceId = DecimalInput.id(connection.sourceId(), "sourceId");
            for (var row :
                    channels.selectList(
                            new LambdaQueryWrapper<CameraChannelEntity>()
                                    .eq(CameraChannelEntity::getSourceId, sourceId)
                                    .in(
                                            CameraChannelEntity::getExternalChannelKey,
                                            externalChannels)))
                existing.put(row.getExternalChannelKey(), row);
            if (input.targetCameraId() != null) {
                if (selected.size() != 1
                        || input.targetCameraVersion() == null
                        || !adapters.supportsCategory(catalog.adapterType(), "DEVICE"))
                    throw invalid();
                var target =
                        channels.selectById(
                                DecimalInput.id(input.targetCameraId(), "targetCameraId"));
                if (target == null || !Objects.equals(target.getSourceId(), sourceId))
                    throw BusinessException.error(ErrorCode.NOT_FOUND);
                if (target.getVersion()
                        != DecimalInput.version(input.targetCameraVersion(), "targetCameraVersion"))
                    throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
                if (!"MANUAL".equals(target.getMappingOrigin())
                        || target.getDeviceId() != null
                        || profiles.selectCount(
                                        new LambdaQueryWrapper<CameraProfileEntity>()
                                                .eq(
                                                        CameraProfileEntity::getChannelId,
                                                        target.getId()))
                                > 0) throw BusinessException.error(ErrorCode.CONFLICT);
                if (group != null && !Objects.equals(group, target.getGroupId())) throw invalid();
                String key = selected.getFirst().channel().externalKey();
                if (existing.containsKey(key) && !existing.get(key).getId().equals(target.getId()))
                    throw BusinessException.error(ErrorCode.CONFLICT);
                existing.put(key, target);
            }
            if (!existing.isEmpty()) {
                var rows =
                        profiles.selectList(
                                new LambdaQueryWrapper<CameraProfileEntity>()
                                        .in(
                                                CameraProfileEntity::getChannelId,
                                                existing.values().stream()
                                                        .map(CameraChannelEntity::getId)
                                                        .toList())
                                        .orderByAsc(CameraProfileEntity::getId));
                if (rows.size() > 8192) throw BusinessException.error(ErrorCode.CONFLICT);
                rows.forEach(
                        p ->
                                existingProfiles
                                        .computeIfAbsent(
                                                p.getChannelId(), ignored -> new ArrayList<>())
                                        .add(p));
            }
            validateDeviceIdentity(catalog.device(), existing.values());
        }
        var normalized =
                selected.stream()
                        .map(
                                s ->
                                        new ImportSelection.Selection(
                                                "c" + s.index(),
                                                s.profileIndexes().stream()
                                                        .map(i -> "p" + i)
                                                        .toList(),
                                                s.defaultIndex() == null
                                                        ? null
                                                        : "p" + s.defaultIndex()))
                        .toList();
        List<Object> existingState = new ArrayList<>();
        existing.forEach(
                (key, c) ->
                        existingState.add(
                                List.of(
                                        key,
                                        c.getId(),
                                        c.getVersion(),
                                        c.getGroupId() == null ? "" : c.getGroupId().toString(),
                                        c.getLifecycle(),
                                        existingProfiles.getOrDefault(c.getId(), List.of()).stream()
                                                .map(
                                                        p ->
                                                                List.of(
                                                                        p.getId(),
                                                                        p.getExternalProfileKey(),
                                                                        p.getVersion()))
                                                .toList())));
        String proof =
                crypto.sign(
                        "camera-access-import-selection",
                        List.of(
                                connection,
                                catalog,
                                new ImportSelection(
                                        normalized,
                                        input.groupId(),
                                        null,
                                        input.targetCameraId(),
                                        input.targetCameraVersion()),
                                existingState));
        return new Prepared(
                selected,
                group,
                existing,
                existingProfiles,
                proof,
                selected.size() - existing.size());
    }

    private void validateDeviceIdentity(
            CameraAccessCatalog.Device observed, Collection<CameraChannelEntity> existing) {
        if (observed == null) return;
        opaque(observed.externalKey(), 512);
        var ids =
                existing.stream()
                        .map(CameraChannelEntity::getDeviceId)
                        .filter(Objects::nonNull)
                        .distinct()
                        .toList();
        if (ids.isEmpty()) return;
        for (var device : devices.selectByIds(ids))
            if (!Objects.equals(device.getExternalDeviceKey(), observed.externalKey()))
                throw BusinessException.error(ErrorCode.CONFLICT);
    }

    private Long ensureDevice(
            long sourceId, CameraAccessCatalog.Device item, Actor actor, LocalDateTime observedAt) {
        if (item == null) return null;
        opaque(item.externalKey(), 512);
        var current =
                devices.selectOne(
                        new LambdaQueryWrapper<CameraDeviceEntity>()
                                .eq(CameraDeviceEntity::getSourceId, sourceId)
                                .eq(CameraDeviceEntity::getExternalDeviceKey, item.externalKey()));
        if (current != null) {
            if (newer(observedAt, current.getInfoObservedAt())) {
                var update =
                        new LambdaUpdateWrapper<CameraDeviceEntity>()
                                .eq(CameraDeviceEntity::getId, current.getId())
                                .eq(CameraDeviceEntity::getVersion, current.getVersion())
                                .and(
                                        q ->
                                                q.isNull(CameraDeviceEntity::getInfoObservedAt)
                                                        .or()
                                                        .lt(
                                                                CameraDeviceEntity
                                                                        ::getInfoObservedAt,
                                                                observedAt))
                                .set(
                                        CameraDeviceEntity::getSourceName,
                                        observedText(item.name(), 128, current.getSourceName()))
                                .set(
                                        CameraDeviceEntity::getManufacturer,
                                        observedText(
                                                item.manufacturer(),
                                                128,
                                                current.getManufacturer()))
                                .set(
                                        CameraDeviceEntity::getModel,
                                        observedText(item.model(), 128, current.getModel()))
                                .set(
                                        CameraDeviceEntity::getFirmwareVersion,
                                        observedText(
                                                item.firmware(), 128, current.getFirmwareVersion()))
                                .set(
                                        CameraDeviceEntity::getSerialNumber,
                                        observedText(
                                                item.serialNumber(),
                                                128,
                                                current.getSerialNumber()))
                                .set(CameraDeviceEntity::getInfoObservedAt, observedAt);
                if (devices.update(null, update) != 1)
                    throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
            }
            return current.getId();
        }
        var row = new CameraDeviceEntity();
        row.setSourceId(sourceId);
        row.setExternalDeviceKey(item.externalKey());
        row.setExternalDeviceRef(item.externalKey());
        row.setDeviceType("UNKNOWN");
        row.setSourceName(optionalText(item.name(), 128));
        row.setManufacturer(optionalText(item.manufacturer(), 128));
        row.setModel(optionalText(item.model(), 128));
        row.setFirmwareVersion(optionalText(item.firmware(), 128));
        row.setSerialNumber(optionalText(item.serialNumber(), 128));
        row.setInfoObservedAt(observedAt);
        row.setVersion(0L);
        row.setCreatedAt(now());
        row.setUpdatedAt(row.getCreatedAt());
        row.setCreatedBy(actor.userId());
        row.setUpdatedBy(actor.userId());
        devices.insert(row);
        return row.getId();
    }

    private CameraProfileEntity insertProfile(
            long sourceId,
            long cameraId,
            CameraAccessCatalog.Profile item,
            String adapterType,
            Actor actor,
            LocalDateTime observedAt) {
        var row = new CameraProfileEntity();
        row.setSourceId(sourceId);
        row.setChannelId(cameraId);
        row.setExternalProfileKey(item.externalKey());
        row.setLabel(display(item.name(), 64, "码流"));
        row.setSourceLabel(optionalText(item.name(), 128));
        String usage = item.usageHint() == null ? "UNKNOWN" : item.usageHint();
        if (!Set.of("MAIN", "SUB", "THIRD", "CUSTOM", "UNKNOWN").contains(usage)) throw invalid();
        row.setUsageHint(usage);
        row.setUsageOrigin("UNKNOWN".equals(usage) ? "UNKNOWN" : "DEVICE_REPORTED");
        row.setEnabled(true);
        row.setVideoCodec(optionalText(item.codec(), 32));
        row.setWidth(positive(item.width()));
        row.setHeight(positive(item.height()));
        row.setBitrateKbps(item.bitrateKbps() == null ? null : (long) positive(item.bitrateKbps()));
        row.setFrameRate(frameRate(item.frameRate()));
        if (!"RTSP".equals(adapterType)) {
            row.setParametersOrigin("CATALOG");
            row.setParametersObservedAt(observedAt);
        }
        row.setVersion(0L);
        row.setCreatedAt(now());
        row.setUpdatedAt(row.getCreatedAt());
        row.setCreatedBy(actor.userId());
        row.setUpdatedBy(actor.userId());
        profiles.insert(row);
        return row;
    }

    /** Catalog metadata cannot overwrite manual settings, media measurements or newer reads. */
    private void refreshProfile(
            CameraProfileEntity current,
            CameraAccessCatalog.Profile item,
            LocalDateTime observedAt) {
        if (!newer(observedAt, current.getParametersObservedAt())
                || current.getParametersOrigin() != null
                        && !Set.of("CATALOG", "UNKNOWN").contains(current.getParametersOrigin()))
            return;
        var update =
                new LambdaUpdateWrapper<CameraProfileEntity>()
                        .eq(CameraProfileEntity::getId, current.getId())
                        .eq(CameraProfileEntity::getVersion, current.getVersion())
                        .and(
                                q ->
                                        q.isNull(CameraProfileEntity::getParametersObservedAt)
                                                .or()
                                                .lt(
                                                        CameraProfileEntity
                                                                ::getParametersObservedAt,
                                                        observedAt))
                        .and(
                                q ->
                                        q.isNull(CameraProfileEntity::getParametersOrigin)
                                                .or()
                                                .in(
                                                        CameraProfileEntity::getParametersOrigin,
                                                        "CATALOG",
                                                        "UNKNOWN"))
                        .set(
                                CameraProfileEntity::getSourceLabel,
                                observedText(item.name(), 128, current.getSourceLabel()))
                        .set(
                                CameraProfileEntity::getVideoCodec,
                                observedText(item.codec(), 32, current.getVideoCodec()))
                        .set(
                                item.width() != null,
                                CameraProfileEntity::getWidth,
                                positive(item.width()))
                        .set(
                                item.height() != null,
                                CameraProfileEntity::getHeight,
                                positive(item.height()))
                        .set(
                                item.bitrateKbps() != null,
                                CameraProfileEntity::getBitrateKbps,
                                positive(item.bitrateKbps()))
                        .set(CameraProfileEntity::getParametersOrigin, "CATALOG")
                        .set(CameraProfileEntity::getParametersObservedAt, observedAt);
        if (item.frameRate() != null) {
            update.set(CameraProfileEntity::getFrameRate, frameRate(item.frameRate()));
        }
        if (profiles.update(null, update) != 1)
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
    }

    private static boolean newer(LocalDateTime observedAt, LocalDateTime previous) {
        // Legacy pending jobs have no reading timestamp and must not refresh existing observations.
        return observedAt != null && (previous == null || observedAt.isAfter(previous));
    }

    private static String observedText(String value, int max, String previous) {
        String next = optionalText(value, max);
        return next == null ? previous : next;
    }

    private static Integer positive(Integer value) {
        if (value != null && value <= 0) throw invalid();
        return value;
    }

    private static BigDecimal frameRate(Double value) {
        if (value == null) return null;
        if (!Double.isFinite(value) || value <= 0 || value > 99999.999) throw invalid();
        return BigDecimal.valueOf(value);
    }

    private static int localIndex(String value, char prefix, int max) {
        if (value == null || !value.matches(prefix + "(?:0|[1-9][0-9]{0,2})")) throw invalid();
        int result = Integer.parseInt(value.substring(1));
        if (result >= max) throw invalid();
        return result;
    }

    private static String opaque(String value, int max) {
        if (value == null
                || value.isEmpty()
                || value.codePointCount(0, value.length()) > max
                || value.codePoints().anyMatch(Character::isISOControl)) throw invalid();
        return value;
    }

    private static String optionalText(String value, int max) {
        if (value == null || value.isBlank()) return null;
        // Observation labels are bounded presentation, never identity keys.
        String safe = value.strip().replaceAll("[\\p{Cc}]", " ");
        return safe.codePointCount(0, safe.length()) > max
                ? safe.substring(0, safe.offsetByCodePoints(0, max))
                : safe;
    }

    private static String display(String value, int max, String fallback) {
        String result = optionalText(value, max);
        return result == null ? fallback : result;
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"));
    }

    private static BusinessException invalid() {
        return CameraSourceRules.invalid();
    }
}
