package com.streamfusion.platform.media.preview.control;

import com.streamfusion.platform.media.preview.PreviewFailure;

/** Explicit admission budgets, not a benchmark or a hardware capability claim. */
public record PreviewBudget(
        int maxSources,
        int maxViewers,
        int maxTranscodes,
        int maxStarting,
        long ingressKbps,
        long egressKbps,
        long sourceUpperKbps,
        long outputUpperKbps) {
    public PreviewBudget {
        if (maxSources < 1
                || maxSources > 64
                || maxViewers < 1
                || maxViewers > 64
                || maxTranscodes < 0
                || maxTranscodes > 64
                || maxStarting < 1
                || maxStarting > 4
                || ingressKbps < 1
                || ingressKbps > 100_000_000
                || egressKbps < 1
                || egressKbps > 100_000_000
                || sourceUpperKbps < 1
                || sourceUpperKbps > 1_000_000
                || outputUpperKbps < sourceUpperKbps
                || outputUpperKbps > 1_000_000)
            throw new IllegalArgumentException("Invalid preview capacity budget");
    }

    public static PreviewBudget defaults(int viewers, int transcodes) {
        return new PreviewBudget(
                viewers, viewers, transcodes, 4, viewers * 10240L, viewers * 10240L, 8192, 8192);
    }

    public long ingressPerSource() {
        return (sourceUpperKbps * 5 + 3) / 4;
    }

    public long egressPerViewer() {
        return (outputUpperKbps * 5 + 3) / 4;
    }

    public record Usage(
            int sources,
            int viewers,
            int transcodes,
            int starting,
            long ingressKbps,
            long egressKbps) {
        public static Usage empty() {
            return new Usage(0, 0, 0, 0, 0, 0);
        }
    }

    public void admit(Usage used, boolean newSource, boolean newTranscode) {
        if (used.sources() + (newSource ? 1 : 0) > maxSources)
            throw new PreviewFailure("NODE_SOURCE_CAPACITY");
        if (used.viewers() + 1 > maxViewers) throw new PreviewFailure("PREVIEW_CAPACITY");
        if (used.starting() + 1 > maxStarting) throw new PreviewFailure("NODE_START_CAPACITY");
        if (used.transcodes() + (newTranscode ? 1 : 0) > maxTranscodes)
            throw new PreviewFailure("TRANSCODE_CAPACITY");
        if (used.ingressKbps() + (newSource ? ingressPerSource() : 0) > ingressKbps)
            throw new PreviewFailure("NODE_INGRESS_CAPACITY");
        if (used.egressKbps() + egressPerViewer() > egressKbps)
            throw new PreviewFailure("NODE_EGRESS_CAPACITY");
    }
}
