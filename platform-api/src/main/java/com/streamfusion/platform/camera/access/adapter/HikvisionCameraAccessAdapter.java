package com.streamfusion.platform.camera.access.adapter;

import static com.streamfusion.platform.camera.access.adapter.CameraXml.*;

import java.net.URI;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import org.springframework.stereotype.Component;
import org.w3c.dom.Element;

/** Native ISAPI directory reader; it does not require ONVIF to be enabled. */
@Component
public class HikvisionCameraAccessAdapter implements CameraAccessAdapter {
    private final CameraHttpTransport transport;

    public HikvisionCameraAccessAdapter(CameraHttpTransport transport) {
        this.transport = transport;
    }

    @Override
    public String type() {
        return "HIKVISION";
    }

    @Override
    public CameraAdapterDescriptor descriptor() {
        return new CameraAdapterDescriptor(
                type(), "海康设备", "DEVICE", "DEVICE_LOGIN", true, 10, false, "", "VENDOR_HTTP");
    }

    @Override
    public CameraAccessCatalog discover(CameraAccessContext context) {
        var session = transport.open(context);
        var infoBytes = session.get(context.endpoint().resolve("/ISAPI/System/deviceInfo"));
        org.w3c.dom.Document info;
        try {
            info = parse(infoBytes);
        } catch (CameraAdapterException ex) {
            // A generic web UI on this path is not evidence that this device implements ISAPI.
            if ("INVALID_XML_RESPONSE".equals(ex.reasonCode()))
                throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
            throw ex;
        }
        if (!"DeviceInfo".equals(info.getDocumentElement().getLocalName())
                || text(info, "model") == null && text(info, "deviceType") == null)
            throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
        var warnings = new LinkedHashSet<String>();
        var device =
                CameraCatalogSupport.device(
                        text(info, "deviceName"),
                        "Hikvision",
                        text(info, "model"),
                        text(info, "firmwareVersion"),
                        text(info, "serialNumber"),
                        warnings);
        var directory = parse(session.get(context.endpoint().resolve("/ISAPI/Streaming/channels")));
        var entries = all(directory, "StreamingChannel");
        if (entries.size() > 2048) throw new CameraAdapterException("DISCOVERY_LIMIT_EXCEEDED");
        Map<String, List<CameraAccessCatalog.Profile>> grouped = new LinkedHashMap<>();
        Map<String, Boolean> mapping = new LinkedHashMap<>();
        var ids = new LinkedHashSet<String>();
        for (Element entry : entries) {
            String id = child(entry, "id");
            if (id == null || !id.matches("[0-9]{1,12}") || !ids.add(id))
                throw new CameraAdapterException("INVALID_PROFILE_IDENTITY");
            Element video = first(entry, "Video");
            if (video == null
                    || "false".equalsIgnoreCase(child(entry, "enabled"))
                    || "false".equalsIgnoreCase(child(video, "enabled"))) continue;
            String channel = text(video, "videoInputChannelID");
            if (channel == null) channel = text(video, "dynVideoInputChannelID");
            boolean unresolved = channel == null;
            if (unresolved) {
                channel = "unmapped-stream:" + id;
                warnings.add("CHANNEL_MAPPING_REQUIRED");
            }
            String usage = text(entry, "streamType");
            usage =
                    "mainStream".equalsIgnoreCase(usage)
                            ? "MAIN"
                            : "subStream".equalsIgnoreCase(usage) ? "SUB" : "UNKNOWN";
            Double rate = decimal(text(video, "maxFrameRate"));
            Integer bitrate = integer(text(video, "constantBitRate"));
            if (bitrate == null) bitrate = integer(text(video, "vbrUpperCap"));
            URI uri = CameraCatalogSupport.rtsp(context, "/Streaming/Channels/" + id, null);
            var profile =
                    new CameraAccessCatalog.Profile(
                            id,
                            child(entry, "channelName"),
                            usage,
                            text(video, "videoCodecType"),
                            integer(text(video, "videoResolutionWidth")),
                            integer(text(video, "videoResolutionHeight")),
                            rate == null ? null : rate / 100.0,
                            bitrate,
                            new CameraAccessCatalog.Locator(type(), uri, id, null));
            grouped.computeIfAbsent(channel, ignored -> new ArrayList<>()).add(profile);
            mapping.put(channel, unresolved);
        }
        if (grouped.isEmpty()) throw new CameraAdapterException("NO_VIDEO_PROFILES");
        var channels = new ArrayList<CameraAccessCatalog.Channel>();
        grouped.forEach(
                (key, value) ->
                        channels.add(
                                new CameraAccessCatalog.Channel(
                                        key, value.getFirst().name(), value, mapping.get(key))));
        return new CameraAccessCatalog(
                type(), device, channels, warnings.isEmpty(), List.copyOf(warnings));
    }
}
