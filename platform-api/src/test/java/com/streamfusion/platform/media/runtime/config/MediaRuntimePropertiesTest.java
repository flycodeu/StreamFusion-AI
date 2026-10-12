package com.streamfusion.platform.media.runtime.config;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.streamfusion.platform.media.runtime.adapter.MediaHostAdapter;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.boot.context.properties.source.MapConfigurationPropertySource;

class MediaRuntimePropertiesTest {
    @Test
    void windowsAndLinuxLocationsCoexistAndOnlyCurrentOsIsSelected() {
        var source =
                new MapConfigurationPropertySource(
                        Map.of(
                                "platform.media-runtime.windows.search-roots[0]", "D:/Media/ZLM",
                                "platform.media-runtime.linux.search-roots[0]", "/opt/zlm"));
        var properties =
                new Binder(source)
                        .bindOrCreate(
                                "platform.media-runtime",
                                Bindable.of(MediaRuntimeProperties.class));
        assertThat(properties.cacheTtl().getSeconds()).isEqualTo(15);
        assertThat(MediaHostAdapter.forOs("Windows 11").paths(properties).searchRoots())
                .containsExactly("D:/Media/ZLM");
        assertThat(MediaHostAdapter.forOs("Linux").paths(properties).searchRoots())
                .containsExactly("/opt/zlm");
        assertThat(MediaHostAdapter.forOs("Mac OS X").os()).isEqualTo("UNSUPPORTED");
    }

    @Test
    void rejectsRelativeDeploymentPaths() {
        assertThatThrownBy(
                        () ->
                                new MediaRuntimeProperties.OsPaths(
                                        java.util.List.of("relative/tools"), null, null))
                .isInstanceOf(IllegalArgumentException.class);
    }
}
