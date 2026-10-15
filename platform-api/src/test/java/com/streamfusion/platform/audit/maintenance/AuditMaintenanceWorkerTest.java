package com.streamfusion.platform.audit.maintenance;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.status.*;
import org.junit.jupiter.api.Test;
import org.springframework.boot.DefaultApplicationArguments;
import org.springframework.context.support.GenericApplicationContext;
import org.springframework.test.util.ReflectionTestUtils;

class AuditMaintenanceWorkerTest {
    @Test
    void nonWebCommandsAndReadOnlyInspectionNeverScheduleMaintenance() {
        var retention = mock(AuditRetentionService.class);
        var exports = mock(AuditExportService.class);
        var status = mock(CameraStatusService.class);
        var plans = mock(CameraStatusPlanService.class);
        try (var context = new GenericApplicationContext()) {
            var auditWorker = new AuditMaintenanceWorker(retention, exports, context);
            var cameraWorker = new CameraStatusMaintenance(status, plans, context);
            try {
                auditWorker.run(new DefaultApplicationArguments());
                cameraWorker.run(new DefaultApplicationArguments());
                verifyNoInteractions(retention, exports, status, plans);
                assertThat(
                                ((java.util.concurrent.ScheduledExecutorService)
                                                ReflectionTestUtils.getField(auditWorker, "worker"))
                                        .isShutdown())
                        .isFalse();
                // Executors are lazily idle: non-Web checks must not even create worker threads.
                assertThat(Thread.getAllStackTraces().keySet())
                        .noneMatch(
                                t ->
                                        t.isAlive()
                                                && (t.getName().equals("audit-maintenance")
                                                        || t.getName()
                                                                .equals("camera-status-cleanup")));
            } finally {
                auditWorker.close();
                cameraWorker.close();
            }
        }
    }
}
