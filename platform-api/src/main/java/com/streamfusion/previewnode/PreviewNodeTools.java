package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.control.PreviewControlTls;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

/** Fixed deployment helpers. They accept validated node configuration, never shell fragments. */
public final class PreviewNodeTools {
    private PreviewNodeTools() {}

    static void run(String action, Path file, ObjectMapper json) throws Exception {
        var config = PreviewNodeConfig.read(file, json);
        if (action.equals("--render")) render(config);
        else if (action.equals("--firewall")) firewall(config);
        else throw new IllegalArgumentException("Unknown node helper");
    }

    static void render(PreviewNodeConfig config) throws Exception {
        Path target = config.zlmConfig();
        if (Files.exists(target)) return; // Never rewrite a live runtime or its secret on restart.
        var values = ini(Path.of("/opt/media/config.ini"));
        set(values, "api", "apiDebug", "0");
        set(values, "api", "secret", PreviewControlTls.secret(config.zlmSecretFile()));
        set(values, "http", "port", Integer.toString(config.zlmApi().getPort()));
        set(values, "http", "sslport", "0");
        set(values, "http", "allow_ip_range", "127.0.0.1,::1");
        set(values, "http", "rootPath", "/opt/preview/empty");
        set(values, "rtsp", "port", "8554");
        set(values, "rtsp", "sslport", "0");
        set(values, "rtsp", "directProxy", "0");
        set(values, "rtmp", "port", "1935");
        set(values, "rtmp", "sslport", "0");
        set(values, "general", "streamNoneReaderDelayMS", "120000");
        set(values, "general", "enable_ffmpeg_log", "0");
        set(values, "rtc", "externIP", config.mediaAddress());
        set(values, "rtc", "port", "18000");
        set(values, "rtc", "tcpPort", "18000");
        set(values, "rtc", "signalingPort", "0");
        set(values, "rtc", "signalingSslPort", "0");
        set(values, "rtc", "enableTurn", "0");
        set(values, "rtp_proxy", "port", "0");
        set(values, "srt", "port", "0");
        set(values, "shell", "port", "0");
        set(values, "hook", "enable", "0");
        set(values, "cluster", "origin_url", "");
        set(values, "ffmpeg", "bin", config.ffmpegExecutable().toString());
        set(values, "ffmpeg", "log", "/dev/null");
        set(
                values,
                "ffmpeg",
                "cmd",
                com.streamfusion.platform.media.preview.media.PreviewEncodingProfile.software());
        set(values, "protocol", "paced_sender_ms", "20");
        // Preserve source audio; each viewer gateway admits only observed compatible tracks.
        set(values, "protocol", "enable_audio", "1");
        StringBuilder text = new StringBuilder();
        values.forEach(
                (section, entries) -> {
                    text.append('[').append(section).append("]\n");
                    entries.forEach(
                            (key, value) ->
                                    text.append(key).append('=').append(value).append('\n'));
                    text.append('\n');
                });
        Files.createDirectories(target.getParent());
        Files.writeString(target, text.toString(), java.nio.file.StandardOpenOption.CREATE_NEW);
        Files.setPosixFilePermissions(
                target, java.nio.file.attribute.PosixFilePermissions.fromString("rw-------"));
    }

    private static void set(
            Map<String, Map<String, String>> values, String section, String key, String value) {
        values.computeIfAbsent(section, ignored -> new LinkedHashMap<>()).put(key, value);
    }

    private static Map<String, Map<String, String>> ini(Path path) throws Exception {
        if (Files.size(path) > 262144)
            throw new IllegalArgumentException("Media configuration too large");
        Map<String, Map<String, String>> values = new LinkedHashMap<>();
        String section = null;
        for (String line : Files.readAllLines(path)) {
            line = line.strip();
            if (line.startsWith("[") && line.endsWith("]"))
                section = line.substring(1, line.length() - 1);
            else if (section != null
                    && !line.startsWith("#")
                    && !line.startsWith(";")
                    && line.contains("=")) {
                int at = line.indexOf('=');
                set(values, section, line.substring(0, at).strip(), line.substring(at + 1).strip());
            }
        }
        return values;
    }

    /** Runs only inside the dedicated Compose network namespace, before either server starts. */
    private static void firewall(PreviewNodeConfig config) throws Exception {
        if (!Files.exists(Path.of("/.dockerenv")))
            throw new IllegalStateException("Node firewall requires the dedicated container");
        var media = ini(config.zlmConfig());
        int rtc = Integer.parseInt(media.get("rtc").get("port"));
        int rtcTcp = Integer.parseInt(media.get("rtc").get("tcpPort"));
        for (String tool : List.of("/usr/sbin/iptables", "/usr/sbin/ip6tables")) {
            command(tool, "-N", "SF_PREVIEW_OUT");
            command(tool, "-A", "SF_PREVIEW_OUT", "-o", "lo", "-j", "ACCEPT");
            command(
                    tool,
                    "-A",
                    "SF_PREVIEW_OUT",
                    "-m",
                    "conntrack",
                    "--ctstate",
                    "ESTABLISHED,RELATED",
                    "-j",
                    "ACCEPT");
            for (String endpoint : config.allowedTargets()) {
                var uri = java.net.URI.create("rtsp://" + endpoint);
                String host = uri.getHost().replace("[", "").replace("]", "");
                if (host.contains(":") != tool.endsWith("ip6tables")) continue;
                command(
                        tool,
                        "-A",
                        "SF_PREVIEW_OUT",
                        "-p",
                        "tcp",
                        "-d",
                        host,
                        "--dport",
                        Integer.toString(uri.getPort()),
                        "-j",
                        "ACCEPT");
            }
            // WebRTC connectivity checks originate from the fixed media port, not arbitrary TCP.
            command(
                    tool,
                    "-A",
                    "SF_PREVIEW_OUT",
                    "-p",
                    "udp",
                    "--sport",
                    Integer.toString(rtc),
                    "-j",
                    "ACCEPT");
            command(tool, "-A", "SF_PREVIEW_OUT", "-j", "REJECT");
            command(tool, "-I", "OUTPUT", "1", "-j", "SF_PREVIEW_OUT");
            command(tool, "-N", "SF_PREVIEW_IN");
            command(tool, "-A", "SF_PREVIEW_IN", "-i", "lo", "-j", "ACCEPT");
            command(
                    tool,
                    "-A",
                    "SF_PREVIEW_IN",
                    "-m",
                    "conntrack",
                    "--ctstate",
                    "ESTABLISHED,RELATED",
                    "-j",
                    "ACCEPT");
            command(
                    tool,
                    "-A",
                    "SF_PREVIEW_IN",
                    "-p",
                    "tcp",
                    "-m",
                    "multiport",
                    "--dports",
                    config.controlPort() + "," + rtcTcp,
                    "-j",
                    "ACCEPT");
            command(
                    tool,
                    "-A",
                    "SF_PREVIEW_IN",
                    "-p",
                    "udp",
                    "--dport",
                    Integer.toString(rtc),
                    "-j",
                    "ACCEPT");
            command(tool, "-A", "SF_PREVIEW_IN", "-j", "REJECT");
            command(tool, "-I", "INPUT", "1", "-j", "SF_PREVIEW_IN");
        }
    }

    private static void command(String executable, String... args) throws Exception {
        var values = new ArrayList<String>();
        values.add(executable);
        values.add("-w");
        values.add("2");
        values.addAll(List.of(args));
        var process =
                new ProcessBuilder(values)
                        .redirectOutput(ProcessBuilder.Redirect.DISCARD)
                        .redirectError(ProcessBuilder.Redirect.DISCARD)
                        .start();
        if (!process.waitFor(3, TimeUnit.SECONDS)) {
            process.destroyForcibly();
            throw new IllegalStateException("Node firewall timed out");
        }
        if (process.exitValue() != 0)
            throw new IllegalStateException(
                    "Node firewall installation failed; refusing to start media");
    }
}
