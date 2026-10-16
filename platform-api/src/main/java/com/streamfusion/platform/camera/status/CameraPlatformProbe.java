package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.access.service.CameraConnectionRules;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Platform facts only: no camera connection and no media pull. */
@Component
@RequiredArgsConstructor
public class CameraPlatformProbe {
    private final CameraSourceMapper sources;
    private final CameraLocatorMapper locators;
    private final CameraSourceService sourceService;
    private final CameraCryptoService crypto;
    private final CameraConnectionRules connections;
    private final HikPlatformApi api;

    public CameraProbeResult execute(
            CameraProfileEntity profile,
            Runnable authorization,
            long deadline,
            RtspDescribeProbe.Cancellation cancellation) {
        return executeBatch(List.of(new Target(profile, authorization, deadline, cancellation)))
                .get(0);
    }

    public record Target(
            CameraProfileEntity profile,
            Runnable authorization,
            long deadline,
            RtspDescribeProbe.Cancellation cancellation) {
        void check() {
            if (cancellation.isCancelled()) throw new CameraAdapterException("CANCELLED");
            if (System.nanoTime() >= deadline)
                throw new CameraAdapterException("PLATFORM_STATUS_TIMEOUT");
            authorization.run();
        }
    }

    /** Each member keeps its own authorization, deadline and result; only transport is shared. */
    public List<CameraProbeResult> executeBatch(List<Target> targets) {
        if (targets.isEmpty()
                || targets.size() > HikPlatformApi.STATUS_BATCH_SIZE
                || targets.stream().map(t -> t.profile().getSourceId()).distinct().count() != 1)
            throw new IllegalArgumentException("Invalid platform status batch");
        long start = System.nanoTime();
        var results = new ArrayList<CameraProbeResult>(Collections.nCopies(targets.size(), null));
        var codes = new LinkedHashMap<Integer, String>();
        try {
            var source = sources.selectById(targets.get(0).profile().getSourceId());
            if (source == null || !"HIK_PLATFORM".equals(source.getAdapterType()))
                throw new CameraAdapterException("CONFIGURATION_CHANGED");
            for (int index = 0; index < targets.size(); index++) {
                var target = targets.get(index);
                try {
                    target.check();
                    var profile = target.profile();
                    var locator = locators.selectById(profile.getId());
                    if (locator == null || !source.getId().equals(locator.getSourceId()))
                        throw new CameraAdapterException("CONFIGURATION_CHANGED");
                    var identity =
                            crypto.decrypt(
                                    "camera_profile_locator/"
                                            + source.getId()
                                            + "/"
                                            + profile.getId()
                                            + "/HIK_PLATFORM",
                                    new CameraCryptoService.Envelope(
                                            locator.getProtocolEncryptionKeyId(),
                                            locator.getProtocolSecretNonce(),
                                            locator.getProtocolSecretCiphertext(),
                                            locator.getProtocolSecretTag()));
                    String code = identity.path("channelKey").asText();
                    HikPlatformApi.requireCode(code);
                    codes.put(index, code);
                } catch (RuntimeException ex) {
                    results.set(index, failed(ex, target, start));
                }
            }
            if (codes.isEmpty()) return results;
            Runnable active =
                    () -> {
                        boolean any = false;
                        for (int index : codes.keySet()) {
                            if (results.get(index) != null) continue;
                            try {
                                targets.get(index).check();
                                any = true;
                            } catch (RuntimeException ex) {
                                results.set(index, failed(ex, targets.get(index), start));
                            }
                        }
                        if (!any) throw new CameraAdapterException("CHECK_NO_LONGER_ACTIVE");
                    };
            active.run();
            var c =
                    connections.normalize(
                            sourceService.resolveConnection(
                                    new CameraConnection(
                                            "HIK_PLATFORM",
                                            null,
                                            null,
                                            null,
                                            null,
                                            null,
                                            null,
                                            null,
                                            null,
                                            source.getId().toString(),
                                            source.getVersion().toString(),
                                            null)));
            active.run();
            // Transport may continue for surviving members; each member still enforces its own
            // deadline through active, including after the response. A cancelled/expired member
            // must not shorten another member's observation window.
            long deadline =
                    codes.keySet().stream()
                            .filter(index -> results.get(index) == null)
                            .mapToLong(index -> targets.get(index).deadline())
                            .max()
                            .orElseThrow();
            var context =
                    new CameraAccessContext(
                            connections.endpoint(c),
                            c.networkPolicyKey(),
                            c.username(),
                            c.password(),
                            c.rtspPort(),
                            active,
                            deadline);
            var requested =
                    codes.entrySet().stream()
                            .filter(e -> results.get(e.getKey()) == null)
                            .map(Map.Entry::getValue)
                            .distinct()
                            .toList();
            Map<String, HikPlatformApi.OnlineStatus> online;
            if (targets.size() == 1) {
                int value = api.online(context, requested.get(0));
                online = Map.of(requested.get(0), new HikPlatformApi.OnlineStatus(value, null));
            } else online = api.onlineBatch(context, requested);
            active.run();
            for (var entry : codes.entrySet()) {
                if (results.get(entry.getKey()) != null) continue;
                var value = online.get(entry.getValue());
                Integer status = value == null ? null : value.value();
                results.set(
                        entry.getKey(),
                        new CameraProbeResult(
                                status == null
                                        ? CameraProbeResult.Outcome.NOT_EXECUTED
                                        : status == 1
                                                ? CameraProbeResult.Outcome.ACCESSIBLE
                                                : CameraProbeResult.Outcome.FAILED,
                                status == null
                                        ? value == null
                                                ? "PLATFORM_STATUS_MISSING"
                                                : value.reasonCode()
                                        : status == 1 ? "PLATFORM_ONLINE" : "PLATFORM_OFFLINE",
                                0,
                                TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start),
                                null));
            }
        } catch (RuntimeException ex) {
            for (int index = 0; index < targets.size(); index++)
                if (results.get(index) == null)
                    results.set(index, failed(ex, targets.get(index), start));
        }
        return List.copyOf(results);
    }

    private static CameraProbeResult failed(RuntimeException ex, Target target, long start) {
        return new CameraProbeResult(
                target.cancellation().isCancelled()
                        ? CameraProbeResult.Outcome.CANCELLED
                        : CameraProbeResult.Outcome.NOT_EXECUTED,
                ex instanceof CameraAdapterException known
                        ? known.reasonCode()
                        : "PLATFORM_STATUS_UNAVAILABLE",
                0,
                TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start),
                null);
    }
}
