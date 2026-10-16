package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.access.service.CameraConnectionRules;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.*;
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
        long start = System.nanoTime();
        try {
            Runnable active =
                    () -> {
                        if (cancellation.isCancelled())
                            throw new CameraAdapterException("CANCELLED");
                        if (System.nanoTime() >= deadline)
                            throw new CameraAdapterException("PLATFORM_STATUS_TIMEOUT");
                        authorization.run();
                    };
            active.run();
            var source = sources.selectById(profile.getSourceId());
            var locator = locators.selectById(profile.getId());
            if (source == null
                    || locator == null
                    || !"HIK_PLATFORM".equals(source.getAdapterType())
                    || !source.getId().equals(locator.getSourceId()))
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
            var context =
                    new CameraAccessContext(
                            connections.endpoint(c),
                            c.networkPolicyKey(),
                            c.username(),
                            c.password(),
                            c.rtspPort(),
                            active,
                            deadline);
            int online = api.online(context, identity.path("channelKey").asText());
            active.run();
            return new CameraProbeResult(
                    online == 1
                            ? CameraProbeResult.Outcome.ACCESSIBLE
                            : CameraProbeResult.Outcome.FAILED,
                    online == 1 ? "PLATFORM_ONLINE" : "PLATFORM_OFFLINE",
                    0,
                    TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start),
                    null);
        } catch (CameraAdapterException ex) {
            return new CameraProbeResult(
                    cancellation.isCancelled()
                            ? CameraProbeResult.Outcome.CANCELLED
                            : CameraProbeResult.Outcome.NOT_EXECUTED,
                    ex.reasonCode(),
                    0,
                    TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start),
                    null);
        } catch (RuntimeException ex) {
            return new CameraProbeResult(
                    CameraProbeResult.Outcome.NOT_EXECUTED,
                    "PLATFORM_STATUS_UNAVAILABLE",
                    0,
                    TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start),
                    null);
        }
    }
}
