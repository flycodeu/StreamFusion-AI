package com.streamfusion.platform.camera.access.service;

import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.service.*;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class CameraPlatformDirectoryService {
    private final CameraAccessService access;
    private final CameraSourceService sources;
    private final CameraConnectionRules connections;
    private final CameraAccessAdapterRegistry adapters;
    private final java.util.concurrent.Semaphore slots = new java.util.concurrent.Semaphore(2);

    public record Query(
            CameraConnection connection, String parentCode, Integer page, Integer size) {}

    public record Region(String indexCode, String parentIndexCode, String name) {}

    public record Regions(List<Region> items, long total, int page, int size) {}

    public Regions regions(Query query) {
        if (!slots.tryAcquire())
            throw com.streamfusion.platform.common.exception.BusinessException.error(
                            com.streamfusion.platform.common.exception.ErrorCode.RATE_LIMITED)
                    .diagnosed("DIRECTORY_BUSY");
        try {
            return readRegions(query);
        } finally {
            slots.release();
        }
    }

    private Regions readRegions(Query query) {
        if (query == null || query.connection() == null) throw CameraSourceRules.invalid();
        var actor = access.readActor();
        access.requireSuper(actor);
        var c = connections.normalize(sources.resolveConnection(query.connection()));
        if (!adapters.supportsCategory(c.method(), "PLATFORM")) throw CameraSourceRules.invalid();
        var adapter = adapters.platform(c.method());
        int page = query.page() == null ? 1 : query.page();
        int size = query.size() == null ? 100 : query.size();
        if (page < 1 || page > 100000 || size < 1 || size > 100) throw CameraSourceRules.invalid();
        var context =
                new CameraAccessContext(
                        connections.endpoint(c),
                        c.networkPolicyKey(),
                        c.username(),
                        c.password(),
                        c.rtspPort(),
                        () -> {
                            var current = access.readActor();
                            access.requireSuper(current);
                            if (current.userId() != actor.userId())
                                throw new CameraAdapterException("ACTOR_REVOKED");
                            if (c.sourceId() != null) sources.resolveConnection(query.connection());
                        },
                        System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(10));
        var result = adapter.regions(context, query.parentCode(), page, size);
        return new Regions(
                result.items().stream()
                        .map(
                                region ->
                                        new Region(
                                                region.indexCode(),
                                                region.parentIndexCode(),
                                                region.name()))
                        .toList(),
                result.total(),
                result.page(),
                result.size());
    }
}
