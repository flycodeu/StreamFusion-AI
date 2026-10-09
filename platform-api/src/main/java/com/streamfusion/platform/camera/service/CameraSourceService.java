package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.dto.*;
import com.streamfusion.platform.camera.pojo.entity.*;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.common.validation.VersionCounter;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class CameraSourceService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraSourceMapper sources;
    private final CameraEndpointMapper endpoints;
    private final CameraCredentialMapper credentials;
    private final CameraLocatorMapper locators;
    private final CameraAccessService access;
    private final CameraSourceRules rules;
    private final CameraProperties properties;
    private final CameraCryptoService crypto;
    private final CameraAccessAdapterRegistry adapters;
    private final CameraCreateService creates;
    private final com.baomidou.mybatisplus.core.incrementer.IdentifierGenerator ids;
    private final AuditService audit;
    private final Clock clock;

    public Map<String, Object> options() {
        access.requireSuper(access.readActor());
        return Map.of(
                "adapterTypes",
                adapters.descriptors().stream().map(d -> d.type()).toList(),
                "credentialPurposes",
                List.of("RTSP", "DEVICE_HTTP", "ONVIF", "VENDOR_HTTP", "PLATFORM_HTTP"),
                "networkPolicies",
                properties.networkPolicyOptions(),
                "ready",
                crypto.ready() && !properties.getNetworkPolicies().isEmpty());
    }

    public boolean storageReady() {
        return crypto.ready();
    }

    public boolean supportsAdapter(String type) {
        return type != null
                && (adapters.supportsCategory(type, "DEVICE")
                        || adapters.supportsCategory(type, "PLATFORM")
                        || adapters.supportsCategory(type, "RTSP"));
    }

    @Transactional(readOnly = true)
    public PageResultVo<Map<String, Object>> page(CameraSourceQueryDto query) {
        access.requireSuper(access.readActor());
        String name = CameraSourceRules.text(query.getName(), 100, false);
        if (query.getAdapterType() != null && !supportsAdapter(query.getAdapterType()))
            throw CameraSourceRules.invalid();
        var filter = new LambdaQueryWrapper<CameraSourceEntity>();
        if (name != null && !name.isEmpty())
            filter.apply(
                    "name LIKE {0} ESCAPE '!'",
                    "%" + name.replace("!", "!!").replace("%", "!%").replace("_", "!_") + "%");
        filter.eq(
                query.getAdapterType() != null,
                CameraSourceEntity::getAdapterType,
                query.getAdapterType());
        filter.eq(query.getEnabled() != null, CameraSourceEntity::getEnabled, query.getEnabled());
        filter.orderByDesc(CameraSourceEntity::getId);
        var page = sources.selectPage(query.toPage(), filter);
        // Bound child reads to this page, rather than one query per row.
        var ids = page.getRecords().stream().map(CameraSourceEntity::getId).toList();
        Map<Long, CameraEndpointEntity> endpointMap = new HashMap<>();
        Map<Long, CameraCredentialEntity> credentialMap = new HashMap<>();
        Set<Long> usedSourceIds = ids.isEmpty() ? Set.of() : sources.sourcesWithChannels(ids);
        Set<Long> boundSourceIds = ids.isEmpty() ? Set.of() : sources.sourcesWithBoundChannels(ids);
        if (!ids.isEmpty()) {
            endpoints
                    .selectList(
                            new LambdaQueryWrapper<CameraEndpointEntity>()
                                    .in(CameraEndpointEntity::getSourceId, ids))
                    .forEach(e -> endpointMap.put(e.getSourceId(), e));
            credentials
                    .selectList(
                            new LambdaQueryWrapper<CameraCredentialEntity>()
                                    .select(
                                            CameraCredentialEntity::getId,
                                            CameraCredentialEntity::getSourceId,
                                            CameraCredentialEntity::getPurpose)
                                    .in(CameraCredentialEntity::getSourceId, ids))
                    .forEach(c -> credentialMap.put(c.getSourceId(), c));
        }
        return PageResultVo.from(
                page,
                page.getRecords().stream()
                        .map(
                                s ->
                                        view(
                                                s,
                                                endpointMap.get(s.getId()),
                                                credentialMap.get(s.getId()),
                                                editable(
                                                        s,
                                                        endpointMap.get(s.getId()),
                                                        usedSourceIds.contains(s.getId()),
                                                        boundSourceIds.contains(s.getId()))))
                        .toList());
    }

    @Transactional(readOnly = true)
    public Map<String, Object> detail(String id) {
        access.requireSuper(access.readActor());
        return view(requireSource(DecimalInput.id(id, "sourceId")));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> create(CameraSourceWriteDto input, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        if (input == null) throw CameraSourceRules.invalid();
        return creates.execute(
                "CREATE_SOURCE",
                null,
                input.clientRequestId(),
                input,
                () -> insertSource(input, actor, context, 554),
                result -> requireSource(Long.parseLong(result.get("sourceId").toString())));
    }

    private Map<String, Object> insertSource(
            CameraSourceWriteDto input,
            CameraAccessService.Actor actor,
            AuditContextDto context,
            Integer mediaPort) {
        String category = category(input.connectionCategory(), input.adapterType());
        if (input.endpoints() == null
                || input.endpoints().size() != 1
                || input.credentials() != null && input.credentials().size() > 1)
            throw CameraSourceRules.invalid();
        var source = new CameraSourceEntity();
        source.setName(CameraSourceRules.text(input.name(), 100, true));
        source.setConnectionCategory(category);
        source.setAdapterType(input.adapterType());
        source.setVendorHint(CameraSourceRules.text(input.vendorHint(), 128, false));
        source.setRtspPort("RTSP".equals(input.adapterType()) ? null : mediaPort);
        if (input.networkPolicyKey() != null) rules.policy(input.networkPolicyKey());
        source.setNetworkPolicyKey(input.networkPolicyKey());
        source.setEnabled(input.enabled() == null || input.enabled());
        source.setRemark(CameraSourceRules.text(input.remark(), 500, false));
        source.setVersion(0L);
        source.setCreatedAt(now());
        source.setUpdatedAt(source.getCreatedAt());
        source.setCreatedBy(actor.userId());
        source.setUpdatedBy(actor.userId());
        sources.insert(source);
        String purpose = endpointPurpose(source, input.endpoints().getFirst());
        CameraCredentialEntity credential = null;
        if (input.credentials() != null && !input.credentials().isEmpty())
            credential =
                    saveCredential(
                            source.getId(), null, input.credentials().getFirst(), false, purpose);
        saveEndpoint(source, null, input.endpoints().getFirst(), credential);
        audit.record(
                actor.userId(),
                "CAMERA_SOURCE",
                source.getId(),
                "CAMERA_SOURCE_CREATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", source.getName()));
        return Map.of("sourceId", source.getId().toString(), "version", "0");
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> update(
            String id, CameraSourceUpdateDto input, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        long sourceId = DecimalInput.id(id, "sourceId");
        long version = DecimalInput.version(input.getVersion(), "version");
        var source = requireSource(sourceId);
        checkVersion(source, version, false);
        var endpoint = endpoint(sourceId);
        if (endpoint == null) throw BusinessException.error(ErrorCode.CONFLICT);
        String purpose = endpoint.getPurpose();
        boolean editable =
                editable(
                        source,
                        endpoint,
                        sources.channelCount(sourceId) > 0,
                        sources.boundChannelCount(sourceId) > 0);
        if (input.hasAdapterType()) {
            if (!"DEVICE_HTTP".equals(purpose) || !editable)
                throw BusinessException.error(ErrorCode.CONFLICT);
            category("DEVICE", input.getAdapterType());
            source.setAdapterType(input.getAdapterType());
        }
        if (input.hasVendorHint())
            source.setVendorHint(CameraSourceRules.text(input.getVendorHint(), 128, false));
        var credential = credential(sourceId);
        if (input.getRtspPort() != null) {
            if ("RTSP".equals(source.getAdapterType())) throw CameraSourceRules.invalid();
            CameraSourceRules.port(input.getRtspPort());
            source.setRtspPort(input.getRtspPort());
        }
        boolean enabling =
                Boolean.TRUE.equals(input.getEnabled())
                        && !Boolean.TRUE.equals(source.getEnabled());
        if (input.getName() != null)
            source.setName(CameraSourceRules.text(input.getName(), 100, true));
        if (input.hasRemark())
            source.setRemark(CameraSourceRules.text(input.getRemark(), 500, false));
        if (input.getEnabled() != null) source.setEnabled(input.getEnabled());
        if (input.getNetworkPolicyKey() != null) {
            rules.policy(input.getNetworkPolicyKey());
            source.setNetworkPolicyKey(input.getNetworkPolicyKey());
            var explicit =
                    locators.selectList(
                            new LambdaQueryWrapper<CameraLocatorEntity>()
                                    .select(CameraLocatorEntity::getRtspHost)
                                    .eq(CameraLocatorEntity::getSourceId, sourceId)
                                    .eq(CameraLocatorEntity::getRtspHostMode, "EXPLICIT"));
            explicit.forEach(l -> rules.host(l.getRtspHost(), source.getNetworkPolicyKey()));
        }
        var upserts = input.getCredentialsUpsert();
        var removes = input.getCredentialsRemove();
        if (upserts != null && upserts.size() > 1
                || removes != null
                        && (removes.size() > 1
                                || removes.stream().anyMatch(p -> !purpose.equals(p))))
            throw CameraSourceRules.invalid();
        boolean remove = removes != null && !removes.isEmpty();
        if (upserts != null && !upserts.isEmpty()) {
            if (remove) throw CameraSourceRules.invalid();
            var command = upserts.getFirst();
            if (command == null) throw CameraSourceRules.invalid();
            boolean clear =
                    command.username() != null && "CLEAR".equals(command.username().action());
            if (clear) {
                if (!purpose.equals(command.purpose())
                        || command.username().value() != null
                        || command.password() == null
                        || !"CLEAR".equals(command.password().action())
                        || command.password().value() != null) throw CameraSourceRules.invalid();
                remove = true;
            } else credential = saveCredential(sourceId, credential, command, true, purpose);
        }
        if (input.getEndpointsRemove() != null && !input.getEndpointsRemove().isEmpty())
            throw CameraSourceRules.invalid();
        var endpointCommands = input.getEndpointsUpsert();
        if (endpointCommands != null && endpointCommands.size() > 1)
            throw CameraSourceRules.invalid();
        if (!editable && endpointCommands != null && !endpointCommands.isEmpty()) {
            var next = endpointCommands.getFirst();
            if (next == null) throw CameraSourceRules.invalid();
            if (!Objects.equals(next.host(), endpoint.getHost())
                    || !Objects.equals(next.port(), endpoint.getPort())
                    || !Objects.equals(next.scheme(), endpoint.getScheme())
                    || !Objects.equals(next.basePath(), endpoint.getBasePath()))
                throw BusinessException.error(ErrorCode.CONFLICT);
        }
        if (endpointCommands != null && !endpointCommands.isEmpty())
            saveEndpoint(source, endpoint, endpointCommands.getFirst(), remove ? null : credential);
        else {
            if (remove && endpoint.getCredentialId() != null) throw CameraSourceRules.invalid();
            if (input.getNetworkPolicyKey() != null || enabling)
                storedEndpointHost(source, endpoint.getPurpose(), endpoint.getHost());
        }
        if (remove && credential != null) credentials.deleteById(credential.getId());
        source.setVersion(VersionCounter.next(version));
        source.setUpdatedAt(now());
        source.setUpdatedBy(actor.userId());
        // Explicit sets preserve nullable remark and make the aggregate CAS visible.
        int changed =
                sources.update(
                        null,
                        new LambdaUpdateWrapper<CameraSourceEntity>()
                                .eq(CameraSourceEntity::getId, sourceId)
                                .eq(CameraSourceEntity::getVersion, version)
                                .set(CameraSourceEntity::getName, source.getName())
                                .set(CameraSourceEntity::getRemark, source.getRemark())
                                .set(CameraSourceEntity::getAdapterType, source.getAdapterType())
                                .set(CameraSourceEntity::getVendorHint, source.getVendorHint())
                                .set(CameraSourceEntity::getEnabled, source.getEnabled())
                                .set(CameraSourceEntity::getRtspPort, source.getRtspPort())
                                .set(
                                        CameraSourceEntity::getNetworkPolicyKey,
                                        source.getNetworkPolicyKey())
                                .set(CameraSourceEntity::getVersion, source.getVersion())
                                .set(CameraSourceEntity::getUpdatedAt, source.getUpdatedAt())
                                .set(CameraSourceEntity::getUpdatedBy, actor.userId()));
        if (changed != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(
                actor.userId(),
                "CAMERA_SOURCE",
                sourceId,
                "CAMERA_SOURCE_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", source.getName()));
        return view(source);
    }

    /** Creates an internal connection aggregate in the caller's asset transaction, without IO. */
    @Transactional(propagation = org.springframework.transaction.annotation.Propagation.MANDATORY)
    public CameraSourceEntity createManualDevice(
            CameraManualConnectionDto connection,
            String name,
            CameraAccessService.Actor actor,
            AuditContextDto context) {
        access.requireSuper(actor);
        if (connection == null
                || (connection.username() == null) != (connection.password() == null))
            throw CameraSourceRules.invalid();
        String scheme = connection.scheme() == null ? "http" : connection.scheme();
        int port = connection.port() == null ? 80 : connection.port();
        boolean anonymous = connection.username() == null;
        var creds =
                anonymous
                        ? List.<CameraSourceWriteDto.Credential>of()
                        : List.of(
                                new CameraSourceWriteDto.Credential(
                                        "DEVICE_HTTP",
                                        new SecretWrite("REPLACE", connection.username()),
                                        new SecretWrite("REPLACE", connection.password())));
        var endpoint =
                new CameraSourceWriteDto.Endpoint(
                        "DEVICE_HTTP",
                        scheme,
                        connection.host(),
                        port,
                        "",
                        anonymous ? "NONE" : "DRIVER_NEGOTIATED",
                        anonymous ? null : "DEVICE_HTTP",
                        "SYSTEM_CA");
        String sourceName =
                name.codePointCount(0, name.length()) > 100
                        ? name.substring(0, name.offsetByCodePoints(0, 100))
                        : name;
        var result =
                insertSource(
                        new CameraSourceWriteDto(
                                null,
                                sourceName,
                                connection.adapterType(),
                                null,
                                true,
                                null,
                                creds,
                                List.of(endpoint),
                                "DEVICE",
                                connection.vendorHint()),
                        actor,
                        context,
                        null);
        return requireSource(Long.parseLong(result.get("sourceId").toString()));
    }

    private String category(String supplied, String type) {
        if (supplied == null) {
            if (type == null) throw CameraSourceRules.invalid();
            if (adapters.supportsCategory(type, "DEVICE")) supplied = "DEVICE";
            else if (adapters.supportsCategory(type, "PLATFORM")) supplied = "PLATFORM";
            else if (adapters.supportsCategory(type, "RTSP")) supplied = "RTSP";
        }
        if (supplied == null
                || !Set.of("DEVICE", "PLATFORM", "RTSP").contains(supplied)
                || (type == null
                        ? !"DEVICE".equals(supplied)
                        : !adapters.supportsCategory(type, supplied)))
            throw CameraSourceRules.invalid();
        return supplied;
    }

    private String endpointPurpose(
            CameraSourceEntity source, CameraSourceWriteDto.Endpoint endpoint) {
        if (endpoint == null) throw CameraSourceRules.invalid();
        if ("DEVICE".equals(source.getConnectionCategory())
                && "DEVICE_HTTP".equals(endpoint.purpose())) return "DEVICE_HTTP";
        return purpose(source.getAdapterType());
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String id, String version, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        long sourceId = DecimalInput.id(id, "sourceId");
        var source = requireSource(sourceId);
        checkVersion(source, DecimalInput.version(version, "version"), true);
        if (sources.channelCount(sourceId) > 0 || sources.deviceCount(sourceId) > 0)
            throw BusinessException.error(ErrorCode.CONFLICT);
        endpoints.delete(
                new LambdaQueryWrapper<CameraEndpointEntity>()
                        .eq(CameraEndpointEntity::getSourceId, sourceId));
        credentials.delete(
                new LambdaQueryWrapper<CameraCredentialEntity>()
                        .eq(CameraCredentialEntity::getSourceId, sourceId));
        sources.deleteById(sourceId);
        audit.record(
                actor.userId(),
                "CAMERA_SOURCE",
                sourceId,
                "CAMERA_SOURCE_DELETE",
                "SUCCESS",
                null,
                context,
                Map.of("name", source.getName()));
    }

    public CameraSourceEntity requireManualSource(long id, Long expectedVersion) {
        var source = requireArchiveSource(id, expectedVersion);
        if (!"RTSP".equals(source.getAdapterType()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        rules.policy(source.getNetworkPolicyKey());
        return source;
    }

    public CameraSourceEntity requireArchiveSource(long id, Long expectedVersion) {
        var source = requireSource(id);
        if (!Boolean.TRUE.equals(source.getEnabled()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        if (expectedVersion != null) checkVersion(source, expectedVersion, false);
        return source;
    }

    private String purpose(String type) {
        if (!supportsAdapter(type)) throw CameraSourceRules.invalid();
        return adapters.descriptor(type).endpointPurpose();
    }

    private String basePath(String type) {
        if (!supportsAdapter(type)) throw CameraSourceRules.invalid();
        return adapters.descriptor(type).endpointPath();
    }

    /** An explicit source ID is the only reuse operation; discovery never edits its credentials. */
    public CameraConnection resolveConnection(CameraConnection input) {
        if (input == null) throw CameraSourceRules.invalid();
        if (input.sourceId() == null) {
            if (input.sourceVersion() != null) throw CameraSourceRules.invalid();
            return input;
        }
        var source = requireSource(DecimalInput.id(input.sourceId(), "sourceId"));
        checkVersion(source, DecimalInput.version(input.sourceVersion(), "sourceVersion"), false);
        if (!Boolean.TRUE.equals(source.getEnabled()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        var endpoint = endpoint(source.getId());
        if (endpoint == null) throw BusinessException.error(ErrorCode.CONFLICT);
        var credential = credential(source.getId());
        String username = null, password = null;
        if (credential != null && endpoint.getCredentialId() != null) {
            var plain =
                    crypto.decrypt(
                            credentialAad(credential),
                            new CameraCryptoService.Envelope(
                                    credential.getEncryptionKeyId(), credential.getSecretNonce(),
                                    credential.getSecretCiphertext(), credential.getSecretTag()));
            username = plain.path("username").asText();
            password = plain.path("password").asText();
        }
        boolean manualUnbound =
                "DEVICE_HTTP".equals(endpoint.getPurpose())
                        && sources.boundChannelCount(source.getId()) == 0;
        String method = source.getAdapterType();
        if (manualUnbound) {
            method = input.method() == null ? (method == null ? "AUTO" : method) : input.method();
            if (!"AUTO".equals(method) && !adapters.supportsCategory(method, "DEVICE"))
                throw CameraSourceRules.invalid();
        } else if (method == null
                || input.method() != null
                        && !"AUTO".equals(input.method())
                        && !method.equals(input.method())) throw CameraSourceRules.invalid();
        sameOptional(input.host(), endpoint.getHost());
        sameOptional(input.port(), endpoint.getPort());
        sameOptional(input.scheme(), endpoint.getScheme());
        sameOptional(input.username(), username);
        sameOptional(input.password(), password);
        String policy = source.getNetworkPolicyKey();
        if (policy == null) policy = input.networkPolicyKey();
        else sameOptional(input.networkPolicyKey(), policy);
        policy = properties.defaultNetworkPolicyKey(policy);
        int mediaPort =
                "RTSP".equals(source.getAdapterType())
                        ? endpoint.getPort()
                        : source.getRtspPort() == null ? 554 : source.getRtspPort();
        sameOptional(input.rtspPort(), mediaPort);
        boolean rtsp = "RTSP".equals(source.getAdapterType());
        if (!rtsp) {
            sameOptional(input.name(), source.getName());
            if (input.rtspUrls() != null && !input.rtspUrls().isEmpty())
                throw CameraSourceRules.invalid();
        }
        rules.host(endpoint.getHost(), policy);
        return new CameraConnection(
                method,
                rtsp ? input.name() : source.getName(),
                endpoint.getHost(),
                endpoint.getPort(),
                endpoint.getScheme(),
                username,
                password,
                mediaPort,
                policy,
                input.sourceId(),
                input.sourceVersion(),
                rtsp ? input.rtspUrls() : null,
                input.pageNumber(),
                input.pageSize());
    }

    private static void sameOptional(Object input, Object saved) {
        if (input != null && !Objects.equals(input, saved)) throw CameraSourceRules.invalid();
    }

    @Transactional(propagation = org.springframework.transaction.annotation.Propagation.MANDATORY)
    public CameraSourceEntity createImported(
            CameraConnection connection,
            String adapterType,
            CameraAccessService.Actor actor,
            AuditContextDto context) {
        access.requireSuper(actor);
        if (connection.sourceId() != null) {
            var resolved = resolveConnection(connection);
            long sourceId = DecimalInput.id(connection.sourceId(), "sourceId");
            if ("DEVICE_HTTP".equals(endpoint(sourceId).getPurpose())) {
                if (!"AUTO".equals(resolved.method()) && !adapterType.equals(resolved.method()))
                    throw CameraSourceRules.invalid();
                return bindImportedAdapter(
                        sourceId,
                        adapterType,
                        DecimalInput.version(connection.sourceVersion(), "sourceVersion"),
                        resolved.networkPolicyKey(),
                        actor,
                        context);
            }
            if (!adapterType.equals(resolved.method())) throw CameraSourceRules.invalid();
            return requireSource(sourceId);
        }
        String purpose = purpose(adapterType);
        boolean anonymous =
                !adapters.supportsCategory(adapterType, "PLATFORM")
                        && connection.username() == null;
        var credential =
                anonymous
                        ? List.<CameraSourceWriteDto.Credential>of()
                        : List.of(
                                new CameraSourceWriteDto.Credential(
                                        purpose,
                                        new SecretWrite("REPLACE", connection.username()),
                                        new SecretWrite("REPLACE", connection.password())));
        var endpoint =
                new CameraSourceWriteDto.Endpoint(
                        purpose,
                        "RTSP".equals(adapterType) ? "rtsp" : connection.scheme(),
                        connection.host(),
                        connection.port(),
                        basePath(adapterType),
                        anonymous ? "NONE" : "DRIVER_NEGOTIATED",
                        anonymous ? null : purpose,
                        "SYSTEM_CA");
        var result =
                insertSource(
                        new CameraSourceWriteDto(
                                null,
                                connection.name(),
                                adapterType,
                                connection.networkPolicyKey(),
                                true,
                                null,
                                credential,
                                List.of(endpoint)),
                        actor,
                        context,
                        connection.rtspPort() == null ? 554 : connection.rtspPort());
        return requireSource(Long.parseLong(result.get("sourceId").toString()));
    }

    /** Explicit import binds a registered device driver, retaining credential purpose and AAD. */
    @Transactional(propagation = org.springframework.transaction.annotation.Propagation.MANDATORY)
    public CameraSourceEntity bindImportedAdapter(
            long sourceId,
            String observedType,
            Long expectedVersion,
            String networkPolicyKey,
            CameraAccessService.Actor actor,
            AuditContextDto context) {
        access.requireSuper(actor);
        var source = requireArchiveSource(sourceId, expectedVersion);
        var endpoint = endpoint(sourceId);
        if (!"DEVICE".equals(source.getConnectionCategory())
                || endpoint == null
                || !"DEVICE_HTTP".equals(endpoint.getPurpose())
                || !adapters.supportsCategory(observedType, "DEVICE"))
            throw CameraSourceRules.invalid();
        rules.host(endpoint.getHost(), networkPolicyKey);
        if (source.getNetworkPolicyKey() != null)
            sameOptional(networkPolicyKey, source.getNetworkPolicyKey());
        boolean changedType = !Objects.equals(source.getAdapterType(), observedType);
        if (!changedType && Objects.equals(source.getNetworkPolicyKey(), networkPolicyKey))
            return source;
        if (changedType && sources.boundChannelCount(sourceId) > 0)
            throw BusinessException.error(ErrorCode.CONFLICT);
        long version = source.getVersion();
        source.setAdapterType(observedType);
        source.setNetworkPolicyKey(networkPolicyKey);
        source.setVersion(VersionCounter.next(version));
        source.setUpdatedAt(now());
        source.setUpdatedBy(actor.userId());
        if (sources.update(
                        null,
                        new LambdaUpdateWrapper<CameraSourceEntity>()
                                .eq(CameraSourceEntity::getId, sourceId)
                                .eq(CameraSourceEntity::getVersion, version)
                                .set(CameraSourceEntity::getAdapterType, observedType)
                                .set(CameraSourceEntity::getNetworkPolicyKey, networkPolicyKey)
                                .set(CameraSourceEntity::getVersion, source.getVersion())
                                .set(CameraSourceEntity::getUpdatedAt, source.getUpdatedAt())
                                .set(CameraSourceEntity::getUpdatedBy, actor.userId()))
                != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(
                actor.userId(),
                "CAMERA_SOURCE",
                sourceId,
                "CAMERA_SOURCE_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", source.getName()));
        return source;
    }

    public CameraSourceEntity requireSource(long id) {
        var source = sources.selectById(id);
        if (source == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return source;
    }

    public String endpointPurpose(long sourceId) {
        var endpoint = endpoint(sourceId);
        if (endpoint == null) throw BusinessException.error(ErrorCode.CONFLICT);
        return endpoint.getPurpose();
    }

    private static void checkVersion(CameraSourceEntity source, long expected, boolean deletion) {
        if (source.getVersion() != expected)
            throw BusinessException.error(
                    deletion ? ErrorCode.PRECONDITION_FAILED : ErrorCode.VERSION_CONFLICT);
    }

    private CameraEndpointEntity endpoint(long sourceId) {
        return endpoints.selectOne(
                new LambdaQueryWrapper<CameraEndpointEntity>()
                        .eq(CameraEndpointEntity::getSourceId, sourceId));
    }

    private CameraCredentialEntity credential(long sourceId) {
        return credentials.selectOne(
                new LambdaQueryWrapper<CameraCredentialEntity>()
                        .eq(CameraCredentialEntity::getSourceId, sourceId));
    }

    private CameraCredentialEntity saveCredential(
            long sourceId,
            CameraCredentialEntity current,
            CameraSourceWriteDto.Credential command,
            boolean update,
            String purpose) {
        if (command == null
                || !purpose.equals(command.purpose())
                || command.username() == null
                || command.password() == null) throw CameraSourceRules.invalid();
        String oldUsername = null, oldPassword = null;
        if (current != null) {
            var plain =
                    crypto.decrypt(
                            credentialAad(current),
                            new CameraCryptoService.Envelope(
                                    current.getEncryptionKeyId(),
                                    current.getSecretNonce(),
                                    current.getSecretCiphertext(),
                                    current.getSecretTag()));
            oldUsername = plain.path("username").asText();
            oldPassword = plain.path("password").asText();
        }
        if (!update
                && (!"REPLACE".equals(command.username().action())
                        || !"REPLACE".equals(command.password().action())))
            throw CameraSourceRules.invalid();
        String username = CameraSourceRules.secret(command.username(), oldUsername, 128, false);
        String password = CameraSourceRules.secret(command.password(), oldPassword, 512, false);
        boolean fresh = current == null;
        if (fresh) {
            current = new CameraCredentialEntity();
            current.setId(ids.nextId(current).longValue());
            current.setSourceId(sourceId);
            current.setPurpose(purpose);
            current.setCreatedAt(now());
        }
        var secret =
                crypto.encrypt(
                        credentialAad(current),
                        Map.of("username", username, "password", password),
                        8192);
        current.setEncryptionKeyId(secret.keyId());
        current.setSecretNonce(secret.nonce());
        current.setSecretCiphertext(secret.ciphertext());
        current.setSecretTag(secret.tag());
        current.setUpdatedAt(now());
        if (fresh) credentials.insert(current);
        else credentials.updateById(current);
        return current;
    }

    private void saveEndpoint(
            CameraSourceEntity source,
            CameraEndpointEntity current,
            CameraSourceWriteDto.Endpoint command,
            CameraCredentialEntity credential) {
        String purpose = current == null ? endpointPurpose(source, command) : current.getPurpose();
        boolean rtsp = "RTSP".equals(source.getAdapterType());
        if (command == null
                || !purpose.equals(command.purpose())
                || !(rtsp
                        ? "rtsp".equals(command.scheme())
                        : "http".equals(command.scheme()) || "https".equals(command.scheme()))
                || !("DEVICE_HTTP".equals(purpose) ? "" : basePath(source.getAdapterType()))
                        .equals(command.basePath())
                || !"SYSTEM_CA".equals(command.tlsPolicy())) throw CameraSourceRules.invalid();
        boolean anonymous = "NONE".equals(command.authMode());
        if (anonymous
                ? "PLATFORM".equals(source.getConnectionCategory())
                        || command.credentialPurpose() != null
                : !"DRIVER_NEGOTIATED".equals(command.authMode())
                        || !purpose.equals(command.credentialPurpose())
                        || credential == null) throw CameraSourceRules.invalid();
        CameraSourceRules.port(command.port());
        String host = storedEndpointHost(source, purpose, command.host());
        boolean fresh = current == null;
        if (fresh) {
            current = new CameraEndpointEntity();
            current.setSourceId(source.getId());
            current.setPurpose(purpose);
            current.setCreatedAt(now());
        }
        current.setScheme(command.scheme());
        current.setHost(host);
        current.setPort(command.port());
        current.setBasePath(command.basePath());
        current.setAuthMode(command.authMode());
        current.setCredentialId(anonymous ? null : credential.getId());
        current.setTlsPolicy("SYSTEM_CA");
        current.setUpdatedAt(now());
        if (fresh) endpoints.insert(current);
        else
            endpoints.update(
                    null,
                    new LambdaUpdateWrapper<CameraEndpointEntity>()
                            .eq(CameraEndpointEntity::getId, current.getId())
                            .set(CameraEndpointEntity::getHost, host)
                            .set(CameraEndpointEntity::getPort, command.port())
                            .set(CameraEndpointEntity::getScheme, command.scheme())
                            .set(CameraEndpointEntity::getBasePath, command.basePath())
                            .set(CameraEndpointEntity::getAuthMode, command.authMode())
                            .set(CameraEndpointEntity::getCredentialId, current.getCredentialId())
                            .set(CameraEndpointEntity::getUpdatedAt, current.getUpdatedAt()));
    }

    private static String credentialAad(CameraCredentialEntity c) {
        return "camera_source_credential/"
                + c.getSourceId()
                + "/"
                + c.getId()
                + "/"
                + c.getPurpose();
    }

    private Map<String, Object> view(CameraSourceEntity s) {
        var endpoint = endpoint(s.getId());
        return view(
                s,
                endpoint,
                credential(s.getId()),
                editable(
                        s,
                        endpoint,
                        sources.channelCount(s.getId()) > 0,
                        sources.boundChannelCount(s.getId()) > 0));
    }

    private Map<String, Object> view(
            CameraSourceEntity s,
            CameraEndpointEntity e,
            CameraCredentialEntity c,
            boolean endpointEditable) {
        var result = new LinkedHashMap<String, Object>();
        result.put("sourceId", s.getId().toString());
        result.put("name", s.getName());
        result.put("connectionCategory", s.getConnectionCategory());
        result.put("adapterType", s.getAdapterType());
        result.put("vendorHint", s.getVendorHint());
        result.put("networkPolicyKey", s.getNetworkPolicyKey());
        result.put("rtspPort", s.getRtspPort());
        result.put("endpointEditable", endpointEditable);
        result.put("enabled", s.getEnabled());
        result.put("remark", s.getRemark());
        result.put("version", s.getVersion().toString());
        result.put("createdAt", s.getCreatedAt().atZone(ZONE).toInstant());
        result.put("updatedAt", s.getUpdatedAt().atZone(ZONE).toInstant());
        if (e != null) {
            var endpoint = new LinkedHashMap<String, Object>();
            endpoint.put("purpose", e.getPurpose());
            endpoint.put("scheme", e.getScheme());
            endpoint.put("host", e.getHost());
            endpoint.put("port", e.getPort());
            endpoint.put("basePath", e.getBasePath());
            endpoint.put("authMode", e.getAuthMode());
            endpoint.put("credentialPurpose", e.getCredentialId() == null ? null : e.getPurpose());
            endpoint.put("tlsPolicy", e.getTlsPolicy());
            result.put("endpoints", List.of(endpoint));
        } else result.put("endpoints", List.of());
        result.put(
                "credentials",
                c == null
                        ? List.of()
                        : List.of(
                                Map.of(
                                        "purpose",
                                        c.getPurpose(),
                                        "configured",
                                        true,
                                        "usernameMasked",
                                        "已配置")));
        return result;
    }

    private String storedEndpointHost(CameraSourceEntity source, String purpose, String host) {
        return "DEVICE_HTTP".equals(purpose) || source.getNetworkPolicyKey() == null
                ? rules.storedHost(host)
                : rules.host(host, source.getNetworkPolicyKey());
    }

    private static boolean editable(
            CameraSourceEntity source,
            CameraEndpointEntity endpoint,
            boolean hasChannels,
            boolean hasBoundChannels) {
        return "RTSP".equals(source.getConnectionCategory())
                || (endpoint != null && "DEVICE_HTTP".equals(endpoint.getPurpose())
                        ? !hasBoundChannels
                        : !hasChannels);
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZONE);
    }
}
