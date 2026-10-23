package com.streamfusion.platform.media.preview.execution;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Transcode;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;

/** One encoded output per upstream/configuration, with independently authorized viewer exits. */
final class PreviewSharedTranscodes {
    private final PreviewMediaGateway media;
    private final PreviewPullRegistry pulls;
    private final Map<Key, Entry> entries = new HashMap<>();
    private final Map<String, Entry> viewers = new HashMap<>();

    PreviewSharedTranscodes(PreviewMediaGateway media, PreviewPullRegistry pulls) {
        this.media = media;
        this.pulls = pulls;
    }

    Stream acquire(String viewer, Stream upstream, PreviewDeadline deadline) {
        var key = new Key(upstream, media.transcodeConfiguration(upstream, deadline));
        Entry entry;
        boolean creator;
        synchronized (this) {
            deadline.check();
            entry = entries.get(key);
            creator = entry == null;
            if (creator) {
                String id = UUID.randomUUID().toString().replace("-", "");
                entry =
                        new Entry(
                                key,
                                new Stream(
                                        upstream.runtime(),
                                        upstream.app(),
                                        id,
                                        "__defaultVhost__/" + upstream.app() + "/" + id));
                entries.put(key, entry);
            }
            if (entry.closing) throw new PreviewFailure("PREVIEW_PENDING");
            entry.members.add(viewer);
            viewers.put(viewer, entry);
        }
        if (creator) {
            try {
                pulls.register(entry.input, true);
                entry.openAttempted = true;
                entry.input =
                        media.openTranscodeInput(
                                upstream, upstream.app(), entry.input.id(), deadline);
                pulls.started(entry.input);
                deadline.check();
                entry.output =
                        media.transcode(
                                entry.input,
                                entry.input.id() + "h264",
                                key.configuration(),
                                deadline);
                deadline.check();
                var output =
                        new Stream(
                                upstream.runtime(),
                                upstream.app(),
                                entry.output.id(),
                                "__defaultVhost__/" + upstream.app() + "/" + entry.output.id());
                entry.ready.complete(output);
            } catch (RuntimeException failure) {
                if (failure instanceof PreviewFailure known && known.pendingTranscode() != null)
                    entry.output = known.pendingTranscode();
                entry.ready.completeExceptionally(failure);
                throw failure;
            }
        }
        try {
            return entry.ready.get(deadline.remaining().toNanos(), TimeUnit.NANOSECONDS);
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.TimeoutException ex) {
            throw new PreviewFailure("PREVIEW_PENDING");
        } catch (ExecutionException ex) {
            if (ex.getCause() instanceof PreviewFailure known) throw known;
            throw new PreviewFailure("PREVIEW_UNAVAILABLE");
        }
    }

    void release(String viewer, PreviewDeadline deadline) {
        Entry entry;
        synchronized (this) {
            entry = viewers.get(viewer);
            if (entry == null) return;
            if (entry.members.size() > 1) {
                entry.members.remove(viewer);
                viewers.remove(viewer);
                return;
            }
            // Keep the final member and ownership journals until exit is confirmed.
            if (!entry.ready.isDone()) throw new PreviewFailure("CLEANUP_PENDING");
            entry.closing = true;
        }
        if (entry.openAttempted) {
            media.close(entry.input, entry.output, deadline);
            pulls.exited(entry.input);
        }
        synchronized (this) {
            entry.members.remove(viewer);
            viewers.remove(viewer);
            entries.remove(entry.key, entry);
        }
    }

    private record Key(Stream upstream, String configuration) {}

    private static final class Entry {
        final Key key;
        final Set<String> members = new HashSet<>();
        final CompletableFuture<Stream> ready = new CompletableFuture<>();
        Stream input;
        Transcode output;
        boolean openAttempted;
        boolean closing;

        Entry(Key key, Stream input) {
            this.key = key;
            this.input = input;
        }
    }
}
