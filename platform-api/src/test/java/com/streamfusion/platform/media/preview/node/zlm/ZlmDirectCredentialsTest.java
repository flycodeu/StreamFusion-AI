package com.streamfusion.platform.media.preview.node.zlm;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.node.PreviewNodeProperties;
import java.nio.file.Files;
import java.nio.file.Path;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ZlmDirectCredentialsTest {
    @TempDir Path home;

    @Test
    void createsAServiceOwnedDirectoryWithoutNodePackageSetup() throws Exception {
        var storage = new ZlmDirectCredentials(new PreviewNodeProperties("", ""), home);

        assertThat(storage.available()).isTrue();
        assertThat(storage.directory())
                .isEqualTo(home.resolve(".streamfusion/preview-zlm-secrets"));
        assertThat(Files.isDirectory(storage.directory())).isTrue();
        try (var files = Files.list(storage.directory())) {
            assertThat(files).isEmpty();
        }
    }

    @Test
    void configuredDirectoryRemainsExplicitAndMustExist() {
        var missing = home.resolve("missing");
        var storage =
                new ZlmDirectCredentials(new PreviewNodeProperties("", missing.toString()), home);

        assertThat(storage.available()).isFalse();
        assertThatThrownBy(storage::directory)
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("NODE_CREDENTIAL_DIRECTORY_MISSING");
    }
}
