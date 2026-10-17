package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewMaintenance;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import java.time.Duration;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Platform grants maintenance only after both the durable reservations and node are empty. */
@Service
@RequiredArgsConstructor
public class PreviewNodeMaintenanceService {
    private final PreviewNodeAdministration administration;
    private final PreviewNodeRepository repository;
    private final PreviewReservationLedger ledger;
    private final PreviewNodeManager manager;
    private final AuditService audit;

    public record Input(String action, String permitId, long version) {}

    public PreviewMaintenance.State inspect(String id) {
        administration.reader();
        var node = remote(id);
        try (var client = manager.client(node.connection())) {
            return client.get(
                    "maintenance",
                    PreviewMaintenance.State.class,
                    PreviewDeadline.after(Duration.ofSeconds(4)));
        }
    }

    public PreviewMaintenance.State apply(String id, Input input, AuditContextDto context) {
        long actor = administration.administrator();
        if (input == null
                || input.action() == null
                || !Set.of("PREPARE", "RELEASE").contains(input.action()))
            throw new PreviewFailure("MAINTENANCE_COMMAND_INVALID");
        // Serializes policy changes in this single-Platform topology. No SQL lock spans HTTP.
        synchronized (manager) {
            var node = remote(id);
            if (!node.mode().equals("DRAINING") || node.version() != input.version())
                throw new PreviewFailure("NODE_DRAIN_REQUIRED");
            var usage = ledger.usage(id);
            if (ledger.hasUnresolvedOperations(id)
                    || usage.sources() != 0
                    || usage.viewers() != 0
                    || usage.transcodes() != 0
                    || usage.starting() != 0
                    || usage.ingressKbps() != 0
                    || usage.egressKbps() != 0) throw new PreviewFailure("NODE_DRAIN_REQUIRED");
            var snapshot = manager.inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
            if (!node.configVersion().equals(snapshot.configVersion()))
                throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
            administration.administrator();
            try (var client = manager.client(node.connection())) {
                var result =
                        client.maintain(
                                new PreviewMaintenance.Command(
                                        1,
                                        id,
                                        snapshot.bootId(),
                                        snapshot.configVersion(),
                                        input.action(),
                                        input.permitId(),
                                        null,
                                        System.currentTimeMillis() + 10000),
                                PreviewDeadline.after(Duration.ofSeconds(10)));
                manager.invalidate(id);
                audit.record(
                        actor,
                        "PREVIEW_NODE",
                        null,
                        "PREVIEW_MAINTENANCE_" + input.action(),
                        "SUCCESS",
                        null,
                        context,
                        Map.of(
                                "code",
                                id,
                                "mode",
                                result.state(),
                                "version",
                                Long.toString(node.version())));
                return result;
            }
        }
    }

    private PreviewNodeRepository.Node remote(String id) {
        var node = repository.node(id);
        if (node == null || node.local()) throw new PreviewFailure("REMOTE_NODE_REQUIRED");
        return node;
    }
}
