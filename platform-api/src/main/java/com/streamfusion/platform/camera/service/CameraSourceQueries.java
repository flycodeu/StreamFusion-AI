package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.mapper.CameraCredentialMapper;
import com.streamfusion.platform.camera.mapper.CameraEndpointMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.pojo.dto.CameraSourceQueryDto;
import com.streamfusion.platform.camera.pojo.entity.CameraCredentialEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraSourceEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraAddressVo;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.time.ZoneId;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/** Reads source configuration and builds credential-safe projections for the camera domain. */
@Service
@RequiredArgsConstructor
public class CameraSourceQueries {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraSourceMapper sources;
    private final CameraEndpointMapper endpoints;
    private final CameraCredentialMapper credentials;
    private final CameraAccessService access;
    private final CameraProperties properties;
    private final CameraCryptoService crypto;
    private final CameraAccessAdapterRegistry adapters;

    public Map<String, Object> options() {
        access.requireSuper(access.readActor());
        return Map.of(
                "adapterTypes",
                adapters.descriptors().stream().map(d -> d.type()).toList(),
                "credentialPurposes",
                List.of("RTSP", "STREAM", "DEVICE_HTTP", "ONVIF", "VENDOR_HTTP", "PLATFORM_HTTP"),
                "networkPolicies",
                properties.networkPolicyOptions(),
                "ready",
                storageReady() && !properties.getNetworkPolicies().isEmpty());
    }

    public boolean storageReady() {
        return crypto.ready();
    }

    /** Display-only lookup after channel authorization/pagination, never a source-directory API. */
    public Map<Long, CameraAddressVo> displayAddresses(List<Long> sourceIds) {
        if (sourceIds.isEmpty()) return Map.of();
        var result = new HashMap<Long, CameraAddressVo>();
        for (var address : endpoints.displayAddresses(sourceIds.stream().distinct().toList())) {
            if (!result.containsKey(address.sourceId()))
                result.put(
                        address.sourceId(),
                        CameraAddressVo.of(address.category(), address.host(), address.port()));
        }
        return result;
    }

    public boolean supportsAdapter(String type) {
        return adapters.supportsSource(type);
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
                                                CameraSourceRules.endpointEditable(
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

    CameraSourceEntity requireSource(long id) {
        var source = sources.selectById(id);
        if (source == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return source;
    }

    CameraEndpointEntity endpoint(long sourceId) {
        return endpoints.selectOne(
                new LambdaQueryWrapper<CameraEndpointEntity>()
                        .eq(CameraEndpointEntity::getSourceId, sourceId));
    }

    Map<String, Object> view(CameraSourceEntity s) {
        var endpoint = endpoint(s.getId());
        return view(
                s,
                endpoint,
                credentials.selectOne(
                        new LambdaQueryWrapper<CameraCredentialEntity>()
                                .select(CameraCredentialEntity::getPurpose)
                                .eq(CameraCredentialEntity::getSourceId, s.getId())),
                CameraSourceRules.endpointEditable(
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
}
