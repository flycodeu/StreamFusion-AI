package com.streamfusion.platform.camera.service;

import java.util.Locale;
import java.util.regex.Pattern;

/** Display/selection hints only. Never derives a channel or profile identity from a name. */
public final class CameraStreamClassification {
    private static final Pattern DAHUA_NAME =
            Pattern.compile("mediaprofile_channel[0-9]+_(mainstream|substream1|substream2)");

    private CameraStreamClassification() {}

    public record Result(String usageHint, String origin, String rule) {}

    public static Result classify(String usage, String origin, String sourceName) {
        if ("MANUAL".equals(origin) || usage != null && !"UNKNOWN".equals(usage))
            return new Result(usage, origin, null);
        String name = sourceName == null ? "" : sourceName.trim().toLowerCase(Locale.ROOT);
        var match = DAHUA_NAME.matcher(name);
        String rule = "ONVIF_STREAM_NAME_V1";
        if (match.matches()) {
            name = match.group(1);
            rule = "DAHUA_PROFILE_NAME_V1";
        }
        String hint =
                switch (name) {
                    case "mainstream" -> "MAIN";
                    case "substream", "substream1" -> "SUB";
                    case "thirdstream", "substream2" -> "THIRD";
                    default -> "UNKNOWN";
                };
        return "UNKNOWN".equals(hint)
                ? new Result("UNKNOWN", "UNKNOWN", null)
                : new Result(hint, "NAME_RULE", rule);
    }
}
