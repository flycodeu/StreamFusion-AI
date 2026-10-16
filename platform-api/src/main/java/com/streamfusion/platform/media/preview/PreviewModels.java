package com.streamfusion.platform.media.preview;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import java.math.BigDecimal;
import java.time.Instant;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

/** Public preview contracts contain metadata and short-lived identifiers, never source locators. */
public final class PreviewModels {
    private PreviewModels() {}

    @Getter
    @Setter
    public static class CatalogQuery extends PageQueryDto {
        private String name;
        private String device;
    }

    public record Profile(
            String streamProfileId,
            String label,
            String usage,
            boolean enabled,
            boolean configured,
            String videoCodec,
            Integer width,
            Integer height,
            BigDecimal frameRate,
            Long bitrateKbps) {}

    public record Channel(
            String cameraId,
            String name,
            String deviceGroupKey,
            String deviceName,
            String groupId,
            String groupPath,
            String lifecycle,
            List<Profile> profiles) {}

    public record CreateLease(
            String clientRequestId,
            String cameraId,
            String streamProfileId,
            String usage,
            List<String> videoCodecs) {}

    public record LeaseView(
            String leaseId,
            String cameraId,
            String streamProfileId,
            String state,
            String reasonCode,
            Instant expiresAt,
            String videoCodec,
            boolean transcoded) {
        public static LeaseView unavailable(String camera, String profile, String reason) {
            return new LeaseView(null, camera, profile, "UNAVAILABLE", reason, null, null, false);
        }
    }

    public record Offer(String sdp) {
        @Override
        public String toString() {
            return "Offer[redacted]";
        }
    }

    public record Answer(String sdp, String type) {
        @Override
        public String toString() {
            return "Answer[redacted]";
        }
    }

    public record Stats(
            String state,
            String videoCodec,
            Integer width,
            Integer height,
            Double frameRate,
            Double mediaKbps,
            Integer readers,
            Instant observedAt) {}
}
