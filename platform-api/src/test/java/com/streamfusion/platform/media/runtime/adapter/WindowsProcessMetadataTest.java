package com.streamfusion.platform.media.runtime.adapter;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.file.Path;
import java.util.Map;
import org.junit.jupiter.api.Test;

class WindowsProcessMetadataTest {
    @Test
    void splitsQuotedWindowsPathsAndFixedZlmArgumentsWithoutShellExpansion() {
        assertThat(
                        WindowsProcessMetadata.splitCommandLine(
                                "\"D:\\Media Server\\MediaServer.exe\" -c \"D:\\Media Config\\config.ini\" -s default.pem"))
                .containsExactly(
                        "D:\\Media Server\\MediaServer.exe",
                        "-c",
                        "D:\\Media Config\\config.ini",
                        "-s",
                        "default.pem");
        assertThat(WindowsProcessMetadata.splitCommandLine("MediaServer.exe --config=config.ini"))
                .containsExactly("MediaServer.exe", "--config=config.ini");
    }

    @Test
    void acceptsCimArgumentsOnlyForTheObservedExecutableAndProcess() throws Exception {
        Path executable = Path.of("MediaServer.exe").toAbsolutePath();
        String data =
                new ObjectMapper()
                        .writeValueAsString(
                                Map.of(
                                        "ProcessId",
                                        12,
                                        "ExecutablePath",
                                        executable.toString(),
                                        "CommandLine",
                                        "\"" + executable + "\" -c config.ini"));
        assertThat(WindowsProcessMetadata.parseResult(data, 12, executable))
                .hasValueSatisfying(args -> assertThat(args).containsExactly("-c", "config.ini"));
        assertThat(WindowsProcessMetadata.parseResult(data, 13, executable)).isEmpty();
        assertThat(
                        WindowsProcessMetadata.parseResult(
                                data, 12, executable.resolveSibling("other.exe")))
                .isEmpty();
        assertThat(WindowsProcessMetadata.parseResult("error", 12, executable)).isEmpty();
    }
}
