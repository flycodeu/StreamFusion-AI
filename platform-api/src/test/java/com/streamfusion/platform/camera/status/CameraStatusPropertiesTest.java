package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatCode;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import org.junit.jupiter.api.Test;

class CameraStatusPropertiesTest {
    @Test
    void defaultsAreBoundedAndLeaveTimeForTimeoutWriteback() {
        var properties = new CameraStatusProperties();
        assertThatCode(properties::validate).doesNotThrowAnyException();
        assertThat(properties.getWorkerThreads()).isEqualTo(4);
        assertThat(properties.getQueueCapacity()).isEqualTo(8);
        assertThat(properties.getStartIntervalMs()).isEqualTo(250);
        assertThat(properties.getProbeTimeoutMs()).isEqualTo(10_000);
        assertThat(properties.getExecutionTimeoutMs()).isEqualTo(30_000);
        assertThat(properties.getExecutionTimeoutMs())
                .isGreaterThan(properties.getProbeTimeoutMs());
    }

    @Test
    void unboundedConcurrencyAndQueueAreRejected() {
        var properties = new CameraStatusProperties();
        properties.setWorkerThreads(9);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
        properties.setWorkerThreads(4);
        properties.setQueueCapacity(33);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void disablingStartBudgetOrPuttingExecutionDeadlineBeforeProbeBudgetIsRejected() {
        var properties = new CameraStatusProperties();
        properties.setStartIntervalMs(0);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
        properties.setStartIntervalMs(250);
        properties.setExecutionTimeoutMs(9_999);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
        properties.setExecutionTimeoutMs(10_000);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void freshnessAndReceiptRetentionCannotBeUnlimited() {
        var properties = new CameraStatusProperties();
        properties.setFreshnessMs(Long.MAX_VALUE);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
        properties.setFreshnessMs(300_000);
        properties.setRetentionDays(0);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
        properties.setRetentionDays(31);
        assertThatThrownBy(properties::validate).isInstanceOf(IllegalArgumentException.class);
    }
}
