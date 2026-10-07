package com.streamfusion.platform.camera.access.adapter;

import static com.streamfusion.platform.camera.access.adapter.CameraXml.*;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import org.springframework.stereotype.Component;

/**
 * Dahua HTTP CGI Encode family: configuration indices are retained, never guessed from resolution.
 */
@Component
public class DahuaCameraAccessAdapter implements CameraAccessAdapter {
    private static final Pattern ENCODE =
            Pattern.compile(
                    "(?:table\\.)?Encode\\[(\\d+)]\\.(MainFormat|ExtraFormat)\\[(\\d+)]\\.(.+)");
    private final CameraHttpTransport transport;

    public DahuaCameraAccessAdapter(CameraHttpTransport transport) {
        this.transport = transport;
    }

    @Override
    public String type() {
        return "DAHUA";
    }

    @Override
    public CameraAdapterDescriptor descriptor() {
        return new CameraAdapterDescriptor(
                type(), "大华设备", "DEVICE", "DEVICE_LOGIN", true, 20, false, "", "VENDOR_HTTP");
    }

    @Override
    public CameraAccessCatalog discover(CameraAccessContext context) {
        var session = transport.open(context);
        byte[] systemBytes =
                session.get(
                        context.endpoint().resolve("/cgi-bin/magicBox.cgi?action=getSystemInfo"));
        Map<String, String> system;
        try {
            system = values(systemBytes);
        } catch (CameraAdapterException ex) {
            if ("INVALID_PROTOCOL_RESPONSE".equals(ex.reasonCode()))
                throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
            throw ex;
        }
        if (system.get("deviceType") == null
                || (system.get("serialNumber") == null
                        && system.get("hardwareVersion") == null
                        && system.get("processor") == null))
            throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
        var warnings = new LinkedHashSet<String>();
        String firmware = system.get("version");
        try {
            firmware =
                    values(
                                    session.get(
                                            context.endpoint()
                                                    .resolve(
                                                            "/cgi-bin/magicBox.cgi?action=getSoftwareVersion")))
                            .get("version");
        } catch (CameraAdapterException ex) {
            if (!"PROTOCOL_NOT_SUPPORTED".equals(ex.reasonCode())) throw ex;
            warnings.add("FIRMWARE_UNAVAILABLE");
        }
        var device =
                CameraCatalogSupport.device(
                        system.get("deviceType"),
                        "Dahua",
                        system.get("deviceType"),
                        firmware,
                        system.get("serialNumber"),
                        warnings);
        Map<String, String> config =
                values(
                        session.get(
                                context.endpoint()
                                        .resolve(
                                                "/cgi-bin/configManager.cgi?action=getConfig&name=Encode")));
        Map<StreamKey, Map<String, String>> streams = new LinkedHashMap<>();
        for (var field : config.entrySet()) {
            var match = ENCODE.matcher(field.getKey());
            if (!match.matches()) continue;
            Integer channel = integer(match.group(1));
            Integer stream = integer(match.group(3));
            if (channel == null || stream == null || channel > 65534 || stream > 31)
                throw new CameraAdapterException("INVALID_PROFILE_IDENTITY");
            boolean main = match.group(2).equals("MainFormat");
            // MainFormat[1/2/3] are event recording modes, not additional live streams.
            if (main && stream != 0) continue;
            streams.computeIfAbsent(
                            new StreamKey(channel, main, stream), ignored -> new LinkedHashMap<>())
                    .put(match.group(4), field.getValue());
        }
        Map<Integer, List<CameraAccessCatalog.Profile>> grouped = new LinkedHashMap<>();
        for (var stream : streams.entrySet()) {
            var key = stream.getKey();
            var fields = stream.getValue();
            if ("false".equalsIgnoreCase(fields.get("VideoEnable"))) continue;
            if (!fields.containsKey("VideoEnable")) warnings.add("PROFILE_ENABLE_UNKNOWN");
            String codec = fields.get("Video.Compression");
            if (codec == null) {
                warnings.add("PROFILE_CONFIGURATION_INCOMPLETE");
                continue;
            }
            // This CGI family defines zero-based Encode indices and one-based RTSP channel numbers.
            int subtype = key.main ? 0 : key.stream + 1;
            String external = key.main ? "MainFormat[0]" : "ExtraFormat[" + key.stream + "]";
            String usage =
                    key.main
                            ? "MAIN"
                            : key.stream == 0 ? "SUB" : key.stream == 1 ? "THIRD" : "CUSTOM";
            var uri =
                    HikvisionCameraAccessAdapter.rtsp(
                            context,
                            "/cam/realmonitor",
                            "channel=" + (key.channel + 1) + "&subtype=" + subtype);
            var profile =
                    new CameraAccessCatalog.Profile(
                            external,
                            external,
                            usage,
                            codec,
                            integer(fields.get("Video.Width")),
                            integer(fields.get("Video.Height")),
                            decimal(fields.get("Video.FPS")),
                            integer(fields.get("Video.BitRate")),
                            new CameraAccessCatalog.Locator(type(), uri, external, subtype));
            grouped.computeIfAbsent(key.channel, ignored -> new ArrayList<>()).add(profile);
        }
        if (grouped.isEmpty()) throw new CameraAdapterException("NO_VIDEO_PROFILES");
        var channels = new ArrayList<CameraAccessCatalog.Channel>();
        grouped.forEach(
                (key, value) ->
                        channels.add(
                                new CameraAccessCatalog.Channel(
                                        "Encode[" + key + "]", "通道 " + (key + 1), value, false)));
        return new CameraAccessCatalog(
                type(),
                device,
                channels,
                !warnings.contains("PROFILE_CONFIGURATION_INCOMPLETE"),
                List.copyOf(warnings));
    }

    private Map<String, String> values(byte[] bytes) {
        Map<String, String> result = new LinkedHashMap<>();
        String text = new String(bytes, StandardCharsets.UTF_8);
        if (text.strip().startsWith("Error"))
            throw new CameraAdapterException("UPSTREAM_PROTOCOL_ERROR");
        for (String line : text.split("\\r?\\n")) {
            int split = line.indexOf('=');
            if (split < 1) continue;
            String key = clean(line.substring(0, split));
            String value = clean(line.substring(split + 1));
            if (result.containsKey(key)) throw new CameraAdapterException("INVALID_PROTOCOL_FIELD");
            result.put(key, value);
        }
        if (result.isEmpty()) throw new CameraAdapterException("INVALID_PROTOCOL_RESPONSE");
        return result;
    }

    private record StreamKey(int channel, boolean main, int stream) {}
}
