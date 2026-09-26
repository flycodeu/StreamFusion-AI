package com.streamfusion.platform.server.adapter;

import jakarta.annotation.PreDestroy;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.time.Duration;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.springframework.stereotype.Component;

/** Native DNS may ignore interruption. One physical lookup and one completion handoff slot only. */
@Component
public class BoundedHostResolver {
    private final Lookup lookup;
    private final ThreadPoolExecutor worker =
            new ThreadPoolExecutor(
                    1,
                    1,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(1),
                    Thread.ofPlatform().daemon().name("server-monitor-dns", 0).factory());
    private CompletableFuture<InetAddress> pending;
    private String pendingHost;
    private boolean resolving;

    public BoundedHostResolver() {
        this(InetAddress::getByName);
    }

    BoundedHostResolver(Lookup lookup) {
        this.lookup = lookup;
    }

    public InetAddress resolve(String host, Duration timeout) {
        if (Thread.currentThread().isInterrupted() || timeout.isNegative() || timeout.isZero())
            return null;
        try {
            InetAddress literal = literal(host);
            if (literal != null) return literal;
        } catch (UnknownHostException | IllegalArgumentException ignored) {
            return null;
        }
        CompletableFuture<InetAddress> result;
        synchronized (this) {
            if (resolving) {
                if (!host.equals(pendingHost)) return null;
                result = pending;
            } else {
                resolving = true;
                pendingHost = host;
                result = new CompletableFuture<>();
                pending = result;
                try {
                    worker.execute(
                            () -> {
                                InetAddress address = null;
                                try {
                                    address = lookup.resolve(host);
                                } catch (UnknownHostException | RuntimeException ignored) {
                                }
                                synchronized (this) {
                                    resolving = false;
                                    result.complete(address);
                                }
                            });
                } catch (RuntimeException ignored) {
                    resolving = false;
                    result.complete(null);
                }
            }
        }
        try {
            return result.get(timeout.toNanos(), TimeUnit.NANOSECONDS);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            return null;
        } catch (ExecutionException | TimeoutException ignored) {
            // Do not cancel: a completed Future is not evidence that native DNS has stopped.
            return null;
        }
    }

    private static InetAddress literal(String host) throws UnknownHostException {
        if (host.contains(":"))
            return InetAddress.getByName(host); // IPv6 syntax cannot trigger DNS.
        if (!host.matches("[0-9.]+")) return null;
        String[] parts = host.split("\\.", -1);
        if (parts.length != 4) throw new UnknownHostException();
        byte[] address = new byte[4];
        for (int i = 0; i < parts.length; i++) {
            int value = Integer.parseInt(parts[i]);
            if (value < 0 || value > 255) throw new UnknownHostException();
            address[i] = (byte) value;
        }
        return InetAddress.getByAddress(address);
    }

    @PreDestroy
    public void close() {
        worker.shutdownNow();
    }

    @FunctionalInterface
    interface Lookup {
        InetAddress resolve(String host) throws UnknownHostException;
    }
}
