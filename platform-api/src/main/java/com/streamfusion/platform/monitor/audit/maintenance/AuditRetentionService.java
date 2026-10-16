package com.streamfusion.platform.monitor.audit.maintenance;

import static com.streamfusion.platform.common.exception.BusinessException.error;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.monitor.audit.mapper.AuditExportMapper;
import com.streamfusion.platform.monitor.audit.mapper.AuditRetentionMapper;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import java.time.*;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionTemplate;

@Service
@RequiredArgsConstructor
public class AuditRetentionService {
    private final AuditRetentionMapper policies;
    private final AuditExportMapper exports;
    private final AuditLogAccess access;
    private final AuditService audit;
    private final PlatformTransactionManager manager;
    private final Clock clock;

    @io.swagger.v3.oas.annotations.media.Schema(name = "AuditRetentionCommand")
    public record Command(
            Boolean enabled, String mode, Integer keepMonths, Integer maxRecords, String version) {}

    @io.swagger.v3.oas.annotations.media.Schema(name = "AuditRetentionView")
    public record View(
            boolean enabled,
            String mode,
            int keepMonths,
            int maxRecords,
            String version,
            Instant nextAt,
            Instant lastStartedAt,
            Instant lastFinishedAt,
            int lastDeleted,
            String lastResult) {}

    @io.swagger.v3.oas.annotations.media.Schema(name = "AuditRetentionPreview")
    public record Preview(
            long total,
            long protectedCount,
            int candidates,
            boolean limited,
            Instant cutoff,
            boolean exportInProgress) {}

    public View get() {
        access.administrator();
        return view(require(policies.selectById(1L)));
    }

    @Transactional
    public View save(Command command, AuditContextDto context) {
        long actor = access.administrator();
        validate(command);
        var row = require(policies.lock());
        if (row.getVersion() != DecimalInput.version(command.version(), "version"))
            throw error(ErrorCode.VERSION_CONFLICT);
        row.setEnabled(command.enabled());
        row.setMode(command.mode());
        row.setKeepMonths(command.keepMonths());
        row.setMaxRecords(command.maxRecords());
        row.setUpdatedBy(actor);
        row.setUpdatedAt(now());
        row.setVersion(row.getVersion() + 1);
        row.setNextAt(Boolean.TRUE.equals(command.enabled()) ? next(row, now()) : null);
        policies.updateById(row);
        audit.record(
                actor,
                "AUDIT_RETENTION",
                1L,
                "AUDIT_RETENTION_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "name",
                        "操作日志保留策略",
                        "enabled",
                        command.enabled().toString(),
                        "mode",
                        command.mode(),
                        "keepMonths",
                        command.keepMonths().toString(),
                        "maxRecords",
                        command.maxRecords().toString()));
        return view(row);
    }

    @Transactional(readOnly = true)
    public Preview preview(Command command) {
        access.administrator();
        validate(command);
        var row = new AuditRetentionEntity();
        row.setMode(command.mode());
        row.setKeepMonths(command.keepMonths());
        row.setMaxRecords(command.maxRecords());
        var boundary = boundary(row, now());
        int candidates =
                boundary == null
                        ? 0
                        : policies.candidates(boundary.createdAt(), boundary.id(), 10001).size();
        return new Preview(
                policies.totalCount(),
                policies.protectedCount(),
                Math.min(10000, candidates),
                candidates > 10000,
                boundary == null ? null : instant(boundary.createdAt()),
                exports.activeCount(now()) > 0);
    }

    @Transactional
    public View requestRun(String version, AuditContextDto context) {
        long actor = access.administrator();
        var row = require(policies.lock());
        if (row.getVersion() != DecimalInput.version(version, "version"))
            throw error(ErrorCode.VERSION_CONFLICT);
        if (!Boolean.TRUE.equals(row.getEnabled()))
            throw error(ErrorCode.CONFLICT).diagnosed("RETENTION_DISABLED");
        if ("RUNNING".equals(row.getLastResult())
                && row.getLastStartedAt() != null
                && row.getLastStartedAt().isAfter(now().minusMinutes(2)))
            throw error(ErrorCode.CONFLICT).diagnosed("RETENTION_RUNNING");
        row.setNextAt(now());
        policies.updateById(row);
        audit.record(actor, "AUDIT_RETENTION", 1L, "AUDIT_RETENTION_RUN", "SUCCESS", null, context);
        return view(row);
    }

    /**
     * One background round, at most ten short transactions. Export/disable is checked per batch.
     */
    public void tick() {
        var tx = new TransactionTemplate(manager);
        var round =
                tx.execute(
                        s -> {
                            var row = require(policies.lock());
                            if ("RUNNING".equals(row.getLastResult())
                                    && row.getLastStartedAt() != null) {
                                if (row.getLastStartedAt().isAfter(now().minusMinutes(2)))
                                    return null;
                                row.setLastResult("INTERRUPTED");
                                row.setLastFinishedAt(now());
                                row.setNextAt(now());
                                policies.updateById(row);
                            }
                            if (!Boolean.TRUE.equals(row.getEnabled())
                                    || row.getNextAt() != null && row.getNextAt().isAfter(now()))
                                return null;
                            if (exports.activeCount(now()) > 0) return null;
                            var started = now();
                            var boundary = boundary(row, started);
                            row.setNextAt(next(row, started));
                            row.setLastStartedAt(started);
                            row.setLastFinishedAt(null);
                            row.setLastDeleted(0);
                            row.setLastResult("RUNNING");
                            policies.updateById(row);
                            return new Round(row.getVersion(), started, boundary);
                        });
        if (round == null) return;
        int deleted = 0;
        String result = "COMPLETED";
        try {
            for (int batch = 0; batch < 10 && round.boundary() != null; batch++) {
                if (Thread.currentThread().isInterrupted()) {
                    result = "STOPPED";
                    break;
                }
                Integer count =
                        tx.execute(
                                s -> {
                                    var policy = require(policies.lock());
                                    if (!Boolean.TRUE.equals(policy.getEnabled())
                                            || !round.started().equals(policy.getLastStartedAt())
                                            || !policy.getVersion().equals(round.version())
                                            || exports.activeCount(now()) > 0) return null;
                                    var ids =
                                            policies.candidates(
                                                    round.boundary().createdAt(),
                                                    round.boundary().id(),
                                                    1000);
                                    return ids.isEmpty() ? 0 : policies.removeCandidates(ids);
                                });
                if (count == null) {
                    result = "PAUSED";
                    break;
                }
                deleted += count;
                if (count < 1000) break;
                if (batch == 9) result = "PARTIAL";
                Thread.sleep(25);
            }
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            result = "STOPPED";
        } catch (RuntimeException ex) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                    .error("Audit retention failed traceId={}", org.slf4j.MDC.get("traceId"), ex);
            result = "FAILED";
        }
        int total = deleted;
        String outcome = result;
        tx.executeWithoutResult(
                s -> {
                    var row = require(policies.lock());
                    if (!round.started().equals(row.getLastStartedAt())) return;
                    row.setLastFinishedAt(now());
                    row.setLastDeleted(total);
                    row.setLastResult(outcome);
                    if (Boolean.TRUE.equals(row.getEnabled())
                            && row.getVersion().equals(round.version())
                            && java.util.Set.of("PARTIAL", "PAUSED", "STOPPED").contains(outcome))
                        row.setNextAt(now().plusMinutes(5));
                    policies.updateById(row);
                    if (total > 0 || "FAILED".equals(outcome))
                        audit.record(
                                null,
                                "AUDIT_RETENTION",
                                1L,
                                "AUDIT_RETENTION_COMPLETE",
                                "FAILED".equals(outcome) ? "FAILURE" : "SUCCESS",
                                "FAILED".equals(outcome) ? "RETENTION_FAILED" : null,
                                null,
                                Map.of(
                                        "name",
                                        "操作日志清理",
                                        "removedLogCount",
                                        Integer.toString(total)));
                });
    }

    private record Round(Long version, LocalDateTime started, AuditBoundary boundary) {}

    private AuditBoundary boundary(AuditRetentionEntity row, LocalDateTime at) {
        if ("AGE".equals(row.getMode()))
            return new AuditBoundary(at.minusMonths(row.getKeepMonths()), null);
        long ordinary = Math.max(1, row.getMaxRecords() - policies.protectedCount());
        return policies.boundary(ordinary - 1);
    }

    private static void validate(Command c) {
        if (c == null
                || c.enabled() == null
                || !java.util.Set.of("AGE", "COUNT").contains(c.mode() == null ? "" : c.mode())
                || c.keepMonths() == null
                || c.keepMonths() < 1
                || c.keepMonths() > 120
                || c.maxRecords() == null
                || c.maxRecords() < 1000
                || c.maxRecords() > 1000000) throw error(ErrorCode.VALIDATION_ERROR);
    }

    private static AuditRetentionEntity require(AuditRetentionEntity row) {
        if (row == null)
            throw error(ErrorCode.DEPENDENCY_UNAVAILABLE).diagnosed("RETENTION_SCHEMA_MISSING");
        return row;
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"))
                .truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    }

    private static Instant instant(LocalDateTime time) {
        return time == null ? null : time.atZone(ZoneId.of("Asia/Shanghai")).toInstant();
    }

    private static LocalDateTime next(AuditRetentionEntity row, LocalDateTime at) {
        if ("COUNT".equals(row.getMode())) return at.plusMinutes(5);
        var next = at.toLocalDate().atTime(3, 0);
        return next.isAfter(at) ? next : next.plusDays(1);
    }

    private static View view(AuditRetentionEntity r) {
        return new View(
                Boolean.TRUE.equals(r.getEnabled()),
                r.getMode(),
                r.getKeepMonths(),
                r.getMaxRecords(),
                r.getVersion().toString(),
                instant(r.getNextAt()),
                instant(r.getLastStartedAt()),
                instant(r.getLastFinishedAt()),
                r.getLastDeleted(),
                r.getLastResult());
    }
}
