package com.streamfusion.platform.media.preview.source;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
import com.streamfusion.platform.camera.ingress.CameraIngressService;
import com.streamfusion.platform.camera.locator.CameraStreamLocator;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import jakarta.annotation.PreDestroy;
import java.net.InetAddress;
import java.net.URI;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Adapts a stored camera target for the preview media transport. */
@Service
@RequiredArgsConstructor
public class PreviewLocatorService {
    private final CameraStreamLocator streams;
    private final CameraSourceRules rules;
    private final CameraIngressService ingress;
    private final ThreadPoolExecutor dns =
            new ThreadPoolExecutor(
                    2,
                    2,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(16),
                    runnable -> {
                        var t = new Thread(runnable, "preview-dns");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());

    @PreDestroy
    public void close() {
        dns.shutdownNow();
    }

    /**
     * Contains credentials in memory only. Avoid default record toString and public serialization.
     */
    public record Resolved(URI uri) {
        @Override
        public String toString() {
            return "Resolved[redacted]";
        }
    }

    public String connectionKey(CameraProfileEntity profile) {
        try {
            return streams.connectionKey(profile);
        } catch (CameraAdapterException ex) {
            throw new PreviewFailure(ex.reasonCode());
        }
    }

    /** Resolve a one-off status observation without a shared connection reservation. */
    public Resolved resolve(
            CameraProfileEntity profile, Runnable authorization, PreviewDeadline deadline) {
        return resolve(profile, null, authorization, deadline);
    }

    public Resolved resolve(
            CameraProfileEntity profile,
            String expectedConnectionKey,
            Runnable authorization,
            PreviewDeadline deadline) {
        CameraStreamLocator.Target target;
        try {
            target =
                    streams.resolve(
                            profile, expectedConnectionKey, authorization, deadline::deadlineNanos);
        } catch (CameraAdapterException ex) {
            throw new PreviewFailure(ex.reasonCode());
        }
        try {
            URI uri = target.uri();
            if (CameraStreamProtocol.ingress(target.kind())) {
                if (ingress == null) throw new PreviewFailure("INGRESS_NOT_CONFIGURED");
                uri = ingress.resolve(target.kind(), uri, authorization, deadline);
            }
            String pinned =
                    pin(
                            uri.getHost().replace("[", "").replace("]", ""),
                            target.networkPolicyKey(),
                            deadline);
            int port =
                    uri.getPort() < 0
                            ? ("https".equals(uri.getScheme())
                                    ? 443
                                    : "http".equals(uri.getScheme())
                                            ? 80
                                            : "rtmp".equals(uri.getScheme()) ? 1935 : 554)
                            : uri.getPort();
            CameraSourceRules.port(port);
            URI authenticated =
                    URI.create(
                            uri.getScheme()
                                    + "://"
                                    + target.authorityAt(pinned, port)
                                    + uri.getRawPath()
                                    + (uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery())
                                    + PinnedMediaUri.fragment(uri));
            authorization.run();
            deadline.check();
            return new Resolved(authenticated);
        } catch (CameraAdapterException ex) {
            deadline.check();
            throw new PreviewFailure(ex.reasonCode());
        } catch (IllegalArgumentException ex) {
            throw new PreviewFailure("LOCATOR_INVALID");
        }
    }

    private String pin(String host, String policy, PreviewDeadline deadline) {
        deadline.check();
        allowedHost(host, policy);
        java.util.concurrent.Future<InetAddress[]> job;
        try {
            job = dns.submit(() -> InetAddress.getAllByName(host));
        } catch (java.util.concurrent.RejectedExecutionException ex) {
            throw new PreviewFailure("DNS_UNAVAILABLE");
        }
        try {
            var addresses =
                    job.get(
                            Math.min(TimeUnit.SECONDS.toNanos(3), deadline.remaining().toNanos()),
                            TimeUnit.NANOSECONDS);
            deadline.check();
            if (addresses.length == 0 || addresses.length > 16)
                throw new PreviewFailure("DESTINATION_REJECTED");
            for (var address : addresses) allowedHost(address.getHostAddress(), policy);
            return addresses[0].getHostAddress();
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.ExecutionException
                | java.util.concurrent.TimeoutException ex) {
            deadline.check();
            throw new PreviewFailure("DNS_UNAVAILABLE");
        } finally {
            job.cancel(true);
        }
    }

    private void allowedHost(String host, String policy) {
        try {
            rules.host(host, policy);
        } catch (BusinessException ex) {
            throw new PreviewFailure("DESTINATION_REJECTED");
        }
    }
}
