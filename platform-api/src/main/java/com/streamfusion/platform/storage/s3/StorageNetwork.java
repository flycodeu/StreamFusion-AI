package com.streamfusion.platform.storage.s3;

import com.streamfusion.platform.storage.StorageProperties;
import com.streamfusion.platform.storage.StorageRules;
import jakarta.annotation.PreDestroy;
import java.net.InetAddress;
import java.net.URI;
import java.util.concurrent.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Resolves once per operation and validates every address before the transport pins a socket. */
@Component
@RequiredArgsConstructor
public class StorageNetwork {
    private final StorageProperties properties;
    private final ThreadPoolExecutor dns =
            new ThreadPoolExecutor(
                    2,
                    2,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(16),
                    runnable -> {
                        var t = new Thread(runnable, "storage-dns");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());

    public void validateEndpoint(URI endpoint) {
        URI origin =
                StorageRules.endpoint(endpoint.getScheme() + "://" + endpoint.getRawAuthority());
        if (properties.getAllowedEndpoints().stream()
                .map(StorageRules::endpoint)
                .noneMatch(origin::equals))
            throw StorageRules.invalid().diagnosed("STORAGE_NETWORK_DENIED");
    }

    public InetAddress resolve(URI endpoint, String legacyPolicy) {
        Future<InetAddress[]> future = null;
        try {
            String host = endpoint.getHost().replace("[", "").replace("]", "");
            validateEndpoint(endpoint);
            future = dns.submit(() -> InetAddress.getAllByName(host));
            var addresses = future.get(3, TimeUnit.SECONDS);
            if (addresses.length == 0 || addresses.length > 16)
                throw new StorageRemoteException("STORAGE_NETWORK_DENIED", 0);
            boolean localHost =
                    host.equalsIgnoreCase("localhost")
                            || host.equals("127.0.0.1")
                            || host.equals("::1");
            for (var address : addresses) {
                if (address.isAnyLocalAddress()
                        || address.isLinkLocalAddress()
                        || address.isMulticastAddress()
                        || address.isLoopbackAddress() && !localHost)
                    throw new StorageRemoteException("STORAGE_NETWORK_DENIED", 0);
            }
            return addresses[0];
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new StorageRemoteException("CANCELLED", 0);
        } catch (Exception ex) {
            throw new StorageRemoteException("STORAGE_NETWORK_DENIED", 0);
        } finally {
            if (future != null) future.cancel(true);
        }
    }

    @PreDestroy
    public void close() {
        dns.shutdownNow();
    }
}
