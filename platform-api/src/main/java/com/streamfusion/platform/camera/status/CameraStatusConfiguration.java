package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.time.Duration;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class CameraStatusConfiguration {
    @Bean(destroyMethod = "close")
    public RtspDescribeProbe cameraStatusProbe(
            CameraSourceRules rules, CameraStatusProperties properties) {
        properties.validate();
        return new RtspDescribeProbe(
                rules,
                new RtspDescribeProbe.Limits(
                        properties.getWorkerThreads(),
                        Math.min(2, properties.getWorkerThreads()),
                        8,
                        16_384,
                        65_536,
                        12_288,
                        16));
    }

    @Bean(destroyMethod = "close")
    public CameraStatusWorker cameraStatusWorker(
            RtspDescribeProbe probe, CameraStatusProperties properties) {
        return new CameraStatusWorker(
                probe,
                new CameraStatusWorker.Limits(
                        properties.getWorkerThreads(),
                        properties.getQueueCapacity(),
                        Duration.ofMillis(properties.getStartIntervalMs())));
    }
}
