package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import com.streamfusion.platform.media.preview.control.PreviewMaintenance;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import java.net.URI;
import java.nio.file.Path;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class PreviewNodeMaintenanceServiceTest {
    final PreviewNodeAdministration authorization = mock(PreviewNodeAdministration.class);
    final PreviewNodeRepository repository = mock(PreviewNodeRepository.class);
    final PreviewReservationLedger ledger = mock(PreviewReservationLedger.class);
    final PreviewNodeManager manager = mock(PreviewNodeManager.class);
    final AuditService audit = mock(AuditService.class);
    final PreviewNodeMaintenanceService service =
            new PreviewNodeMaintenanceService(authorization, repository, ledger, manager, audit);
    final String permit = UUID.randomUUID().toString();

    PreviewNodeRepository.Node node(String mode) {
        return new PreviewNodeRepository.Node(
                new PreviewNodeConnection(
                        "a",
                        "A",
                        URI.create("https://localhost:9443"),
                        "c".repeat(64),
                        Path.of("test-token").toAbsolutePath(),
                        "192.0.2.10"),
                mode,
                "v",
                3);
    }

    @Test
    void onlyEmptyRemoteDrainingNodeCanObtainPermit() {
        when(repository.node("a")).thenReturn(node("REMOTE"));
        var input = new PreviewNodeMaintenanceService.Input("PREPARE", permit, 3);
        assertThatThrownBy(() -> service.apply("a", input, mock(AuditContextDto.class)))
                .hasMessage("NODE_DRAIN_REQUIRED");
        when(repository.node("a")).thenReturn(node("DRAINING"));
        when(ledger.usage("a")).thenReturn(new PreviewBudget.Usage(1, 0, 0, 0, 0, 0));
        assertThatThrownBy(() -> service.apply("a", input, mock(AuditContextDto.class)))
                .hasMessage("NODE_DRAIN_REQUIRED");
        verify(manager, never()).client(any());
        when(repository.node("local"))
                .thenReturn(new PreviewNodeRepository.Node(null, "DRAINING", "v", 3));
        assertThatThrownBy(() -> service.apply("local", input, mock(AuditContextDto.class)))
                .hasMessage("REMOTE_NODE_REQUIRED");
    }

    @Test
    void rechecksAuthorityAndAuditsPermitWithoutAutomaticallyEnabling() {
        when(repository.node("a")).thenReturn(node("DRAINING"));
        when(ledger.usage("a")).thenReturn(PreviewBudget.Usage.empty());
        when(manager.inspect(any(), any()))
                .thenReturn(
                        new PreviewControl.Snapshot(
                                1,
                                "a",
                                "boot",
                                "preview-control-1",
                                "v",
                                "READY",
                                64,
                                64,
                                0,
                                0,
                                List.of()));
        var client = mock(PreviewNodeClient.class);
        when(manager.client(any())).thenReturn(client);
        when(client.maintain(any(), any()))
                .thenReturn(
                        new PreviewMaintenance.State(
                                1, "a", "SEALED", permit, "boot", "v", null, 100));
        var result =
                service.apply(
                        "a",
                        new PreviewNodeMaintenanceService.Input("PREPARE", permit, 3),
                        mock(AuditContextDto.class));
        assertThat(result.state()).isEqualTo("SEALED");
        verify(authorization, times(2)).administrator();
        verify(client).close();
        verify(manager).invalidate("a");
        verify(repository, never()).policy(anyString(), anyString(), anyInt(), any(), anyLong());
        verify(audit)
                .record(
                        anyLong(),
                        eq("PREVIEW_NODE"),
                        isNull(),
                        eq("PREVIEW_MAINTENANCE_PREPARE"),
                        eq("SUCCESS"),
                        isNull(),
                        any(),
                        anyMap());
    }
}
