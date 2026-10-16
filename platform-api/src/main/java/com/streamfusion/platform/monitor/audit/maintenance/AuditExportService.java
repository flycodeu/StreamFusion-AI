package com.streamfusion.platform.monitor.audit.maintenance;

import static com.streamfusion.platform.common.exception.BusinessException.error;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.incrementer.DefaultIdentifierGenerator;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.monitor.audit.mapper.AuditExportMapper;
import com.streamfusion.platform.monitor.audit.mapper.AuditReadMapper;
import com.streamfusion.platform.monitor.audit.mapper.AuditRetentionMapper;
import com.streamfusion.platform.monitor.audit.pojo.dto.*;
import com.streamfusion.platform.monitor.audit.service.*;
import jakarta.servlet.http.HttpServletResponse;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionTemplate;

@Service
@RequiredArgsConstructor
public class AuditExportService {
    private final AuditExportMapper jobs;
    private final AuditRetentionMapper policies;
    private final AuditReadMapper rows;
    private final AuditQueryService query;
    private final AuditChanges changes;
    private final AuditLogAccess access;
    private final AuditService audit;
    private final AuditExportStorage storage;
    private final ObjectMapper json;
    private final PlatformTransactionManager manager;
    private final Clock clock;
    private final String worker = UUID.randomUUID().toString().replace("-", "");

    @io.swagger.v3.oas.annotations.media.Schema(name = "AuditExportCommand")
    public record Command(String clientRequestId, String format, AuditQueryDto filter) {}

    @io.swagger.v3.oas.annotations.media.Schema(name = "AuditExportView")
    public record View(
            String id,
            String actorId,
            String state,
            String format,
            int rowCount,
            long fileBytes,
            String reasonCode,
            Instant createdAt,
            Instant finishedAt,
            Instant expiresAt,
            boolean downloadable,
            AuditCriteria filter) {}

    @Transactional
    public View create(Command command, AuditContextDto context) {
        long actor = access.actor();
        if (command == null
                || command.clientRequestId() == null
                || !command.clientRequestId().matches("[a-f0-9-]{36}")
                || !Set.of("CSV", "JSONL")
                        .contains(command.format() == null ? "" : command.format()))
            throw error(ErrorCode.VALIDATION_ERROR);
        var criteria = query.criteria(command.filter());
        String filter = encode(criteria);
        policies.lock();
        var prior =
                jobs.selectOne(
                        new LambdaQueryWrapper<AuditExportEntity>()
                                .eq(AuditExportEntity::getActorId, actor)
                                .eq(
                                        AuditExportEntity::getClientRequestId,
                                        command.clientRequestId()));
        if (prior != null) {
            if (!prior.getFilterJson().equals(filter)
                    || !prior.getFormat().equals(command.format())) throw error(ErrorCode.CONFLICT);
            return view(prior);
        }
        if (jobs.activeCount(now()) >= 4 || jobs.liveFiles() >= 5)
            throw error(ErrorCode.RATE_LIMITED).diagnosed("EXPORT_CAPACITY_REACHED");
        var first = rows.exportBatch(criteria, null, null, null, null);
        var row = new AuditExportEntity();
        row.setId(DefaultIdentifierGenerator.getInstance().nextId(row).longValue());
        row.setActorId(actor);
        row.setClientRequestId(command.clientRequestId());
        row.setFilterJson(filter);
        row.setFormat(command.format());
        row.setState("QUEUED");
        row.setStorageKey(storage.key());
        row.setCreatedAt(now());
        row.setExpiresAt(now().plusHours(24));
        row.setRowCount(0);
        row.setFileBytes(0L);
        row.setFileRemoved(false);
        if (!first.isEmpty()) {
            row.setUpperTime(first.getFirst().getCreatedAt());
            row.setUpperId(first.getFirst().getId());
        }
        jobs.insert(row);
        audit.record(
                actor,
                "AUDIT_EXPORT",
                row.getId(),
                "AUDIT_EXPORT_CREATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", "操作日志导出", "format", row.getFormat()));
        return view(row);
    }

    public List<View> list() {
        long actor = access.actor();
        return jobs
                .selectList(
                        new LambdaQueryWrapper<AuditExportEntity>()
                                .eq(!access.superAdmin(actor), AuditExportEntity::getActorId, actor)
                                .orderByDesc(AuditExportEntity::getCreatedAt)
                                .orderByDesc(AuditExportEntity::getId)
                                .last("LIMIT 100"))
                .stream()
                .map(this::view)
                .toList();
    }

    public View detail(String id) {
        return view(owned(id));
    }

    @Transactional
    public View cancel(String id, AuditContextDto context) {
        policies.lock();
        var row = owned(id);
        if (Set.of("QUEUED", "RUNNING").contains(row.getState())) {
            row.setState("CANCELLED");
            row.setFinishedAt(now());
            jobs.updateById(row);
            audit.record(
                    access.actor(),
                    "AUDIT_EXPORT",
                    row.getId(),
                    "AUDIT_EXPORT_CANCEL",
                    "SUCCESS",
                    null,
                    context);
        }
        return view(row);
    }

    @Transactional
    public View remove(String id, AuditContextDto context) {
        policies.lock();
        var row = owned(id);
        if (!row.getStorageKey().equals(storage.key())
                || Set.of("QUEUED", "RUNNING").contains(row.getState()))
            throw error(ErrorCode.CONFLICT);
        try {
            storage.remove(row.getId());
        } catch (IOException ex) {
            throw error(ErrorCode.CONFLICT).diagnosed("EXPORT_FILE_BUSY");
        }
        row.setState("EXPIRED");
        row.setFileRemoved(true);
        jobs.updateById(row);
        audit.record(
                access.actor(),
                "AUDIT_EXPORT",
                row.getId(),
                "AUDIT_EXPORT_DELETE",
                "SUCCESS",
                null,
                context);
        return view(row);
    }

    public void download(String id, HttpServletResponse response, AuditContextDto context)
            throws IOException {
        var row = owned(id);
        if (!view(row).downloadable())
            throw error(ErrorCode.CONFLICT).diagnosed("EXPORT_NOT_AVAILABLE");
        AuditExportStorage.Download file;
        try {
            file = storage.open(row.getId());
        } catch (IOException ex) {
            throw error(ErrorCode.CONFLICT).diagnosed("EXPORT_NOT_AVAILABLE");
        }
        try (file) {
            response.setContentType(
                    "CSV".equals(row.getFormat())
                            ? "text/csv;charset=UTF-8"
                            : "application/x-ndjson;charset=UTF-8");
            response.setHeader(
                    "Content-Disposition",
                    "attachment; filename=\"audit-"
                            + row.getId()
                            + ("CSV".equals(row.getFormat()) ? ".csv" : ".jsonl")
                            + "\"");
            response.setHeader("Cache-Control", "no-store");
            response.setHeader("X-Content-Type-Options", "nosniff");
            response.setContentLengthLong(row.getFileBytes());
            file.copyTo(response.getOutputStream());
        }
        audit.record(
                access.actor(),
                "AUDIT_EXPORT",
                row.getId(),
                "AUDIT_EXPORT_DOWNLOAD",
                "SUCCESS",
                null,
                context);
    }

    /** Called only by the maintenance worker. A persisted lease fences lost workers. */
    public boolean workOne() {
        var tx = new TransactionTemplate(manager);
        var job =
                tx.execute(
                        s -> {
                            policies.lock();
                            jobs.expireLeases(now());
                            var row = jobs.next(storage.key(), now());
                            if (row == null) return null;
                            row.setState("RUNNING");
                            row.setWorkerId(worker);
                            row.setStartedAt(now());
                            row.setLeaseUntil(now().plusMinutes(2));
                            jobs.updateById(row);
                            return row;
                        });
        if (job == null) return false;
        try {
            if (!access.exportAllowed(job.getActorId()))
                throw new ExportFailure("EXPORT_PERMISSION_REVOKED");
            var criteria = json.readValue(job.getFilterJson(), AuditCriteria.class);
            long deadline = System.nanoTime() + Duration.ofMinutes(10).toNanos();
            int count = 0;
            LocalDateTime afterTime = null;
            Long afterId = null;
            try (var output =
                    new BufferedWriter(
                            new OutputStreamWriter(
                                    storage.create(job.getId()), StandardCharsets.UTF_8),
                            16384)) {
                if ("CSV".equals(job.getFormat()))
                    output.write(
                            "\ufeff记录ID,操作时间,操作人,账号ID,目标类型编码,对象名称,对象ID,动作编码,结果,原因码,请求标识,变更摘要\r\n");
                while (job.getUpperId() != null) {
                    if (Thread.currentThread().isInterrupted() || System.nanoTime() > deadline)
                        throw new ExportFailure("EXPORT_TIMEOUT");
                    if (!access.exportAllowed(job.getActorId()))
                        throw new ExportFailure("EXPORT_PERMISSION_REVOKED");
                    if (!heartbeat(job.getId(), count)) throw new ExportFailure("EXPORT_CANCELLED");
                    var batch =
                            rows.exportBatch(
                                    criteria,
                                    job.getUpperTime(),
                                    job.getUpperId(),
                                    afterTime,
                                    afterId);
                    if (batch.isEmpty()) break;
                    if (count + batch.size() > 100000) throw new ExportFailure("EXPORT_ROW_LIMIT");
                    if (batch.stream().anyMatch(r -> Boolean.TRUE.equals(r.getExportOversize())))
                        throw new ExportFailure("EXPORT_ROW_TOO_LARGE");
                    var entries = query.entries(batch);
                    for (int i = 0; i < batch.size(); i++) {
                        var entry = entries.get(i);
                        var selected = changes.read(batch.get(i).getChanges()).fields();
                        if ("JSONL".equals(job.getFormat()))
                            output.write(
                                    json.writeValueAsString(
                                                    Map.of(
                                                            "schemaVersion",
                                                            1,
                                                            "record",
                                                            entry,
                                                            "changes",
                                                            selected))
                                            + "\n");
                        else {
                            String actor =
                                    entry.actor() == null ? entry.username() : entry.actor().name();
                            String target = entry.target() == null ? null : entry.target().name();
                            output.write(
                                    String.join(
                                                    ",",
                                                    Arrays.asList(
                                                                    entry.id(),
                                                                    entry.createdAt().toString(),
                                                                    actor,
                                                                    entry.actorId(),
                                                                    entry.module(),
                                                                    target,
                                                                    entry.targetId(),
                                                                    entry.action(),
                                                                    entry.result(),
                                                                    entry.reasonCode(),
                                                                    entry.traceId(),
                                                                    json.writeValueAsString(
                                                                            selected))
                                                            .stream()
                                                            .map(AuditExportStorage::csv)
                                                            .toList())
                                            + "\r\n");
                        }
                    }
                    count += batch.size();
                    var last = batch.getLast();
                    afterTime = last.getCreatedAt();
                    afterId = last.getId();
                    Thread.sleep(10);
                }
            }
            int completed = count;
            tx.executeWithoutResult(
                    s -> {
                        policies.lock();
                        var row = jobs.selectById(job.getId());
                        if (!running(row)) throw new ExportFailure("EXPORT_CANCELLED");
                        if (!access.exportAllowed(row.getActorId()))
                            throw new ExportFailure("EXPORT_PERMISSION_REVOKED");
                        try {
                            row.setFileBytes(storage.publish(row.getId()));
                        } catch (IOException ex) {
                            throw new ExportFailure("EXPORT_IO_ERROR");
                        }
                        row.setRowCount(completed);
                        row.setState("COMPLETED");
                        row.setFinishedAt(now());
                        row.setLeaseUntil(null);
                        jobs.updateById(row);
                        audit.record(
                                row.getActorId(),
                                "AUDIT_EXPORT",
                                row.getId(),
                                "AUDIT_EXPORT_COMPLETE",
                                "SUCCESS",
                                null,
                                null,
                                Map.of(
                                        "name",
                                        "操作日志导出",
                                        "rowCount",
                                        Integer.toString(completed),
                                        "fileBytes",
                                        row.getFileBytes().toString()));
                    });
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            fail(job, "WORKER_STOPPED");
        } catch (Exception ex) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                    .warn(
                            "Audit export failed traceId={} exportId={} errorType={}",
                            org.slf4j.MDC.get("traceId"),
                            job.getId(),
                            ex.getClass().getSimpleName());
            fail(job, ex instanceof ExportFailure f ? f.reason : "EXPORT_IO_ERROR");
        }
        return true;
    }

    private boolean heartbeat(long id, int count) {
        return jobs.update(
                        null,
                        new LambdaUpdateWrapper<AuditExportEntity>()
                                .eq(AuditExportEntity::getId, id)
                                .eq(AuditExportEntity::getState, "RUNNING")
                                .eq(AuditExportEntity::getWorkerId, worker)
                                .gt(AuditExportEntity::getLeaseUntil, now())
                                .set(AuditExportEntity::getLeaseUntil, now().plusMinutes(2))
                                .set(AuditExportEntity::getRowCount, count))
                == 1;
    }

    public void expireFiles() {
        for (var row :
                jobs.selectList(
                        new LambdaQueryWrapper<AuditExportEntity>()
                                .eq(AuditExportEntity::getStorageKey, storage.key())
                                .eq(AuditExportEntity::getFileRemoved, false)
                                .and(
                                        w ->
                                                w.le(AuditExportEntity::getExpiresAt, now())
                                                        .or()
                                                        .in(
                                                                AuditExportEntity::getState,
                                                                "FAILED",
                                                                "CANCELLED"))
                                .last("LIMIT 20"))) {
            if ("RUNNING".equals(row.getState())
                    && row.getLeaseUntil() != null
                    && row.getLeaseUntil().isAfter(now())) continue;
            try {
                storage.remove(row.getId());
            } catch (IOException ex) {
                continue;
            }
            jobs.update(
                    null,
                    new LambdaUpdateWrapper<AuditExportEntity>()
                            .eq(AuditExportEntity::getId, row.getId())
                            .eq(AuditExportEntity::getState, row.getState())
                            .set(
                                    AuditExportEntity::getState,
                                    row.getExpiresAt().isAfter(now()) ? row.getState() : "EXPIRED")
                            .set(AuditExportEntity::getFileRemoved, true));
        }
    }

    private void fail(AuditExportEntity job, String reason) {
        jobs.update(
                null,
                new LambdaUpdateWrapper<AuditExportEntity>()
                        .eq(AuditExportEntity::getId, job.getId())
                        .eq(AuditExportEntity::getState, "RUNNING")
                        .eq(AuditExportEntity::getWorkerId, worker)
                        .set(AuditExportEntity::getState, "FAILED")
                        .set(AuditExportEntity::getReasonCode, reason)
                        .set(AuditExportEntity::getFinishedAt, now())
                        .set(AuditExportEntity::getLeaseUntil, null));
        try {
            storage.remove(job.getId());
            jobs.update(
                    null,
                    new LambdaUpdateWrapper<AuditExportEntity>()
                            .eq(AuditExportEntity::getId, job.getId())
                            .set(AuditExportEntity::getFileRemoved, true));
        } catch (IOException ignored) {
        }
    }

    private boolean running(AuditExportEntity row) {
        return row != null
                && "RUNNING".equals(row.getState())
                && worker.equals(row.getWorkerId())
                && row.getLeaseUntil() != null
                && row.getLeaseUntil().isAfter(now());
    }

    private AuditExportEntity owned(String id) {
        long actor = access.actor();
        var row = jobs.selectById(DecimalInput.id(id, "exportId"));
        if (row == null || row.getActorId() != actor && !access.superAdmin(actor))
            throw error(ErrorCode.NOT_FOUND);
        return row;
    }

    private View view(AuditExportEntity r) {
        try {
            return new View(
                    r.getId().toString(),
                    r.getActorId().toString(),
                    r.getState(),
                    r.getFormat(),
                    r.getRowCount(),
                    r.getFileBytes(),
                    r.getReasonCode(),
                    instant(r.getCreatedAt()),
                    instant(r.getFinishedAt()),
                    instant(r.getExpiresAt()),
                    "COMPLETED".equals(r.getState())
                            && r.getExpiresAt().isAfter(now())
                            && storage.key().equals(r.getStorageKey())
                            && storage.available(r.getId()),
                    json.readValue(r.getFilterJson(), AuditCriteria.class));
        } catch (IOException ex) {
            throw error(ErrorCode.INTERNAL_ERROR);
        }
    }

    private String encode(Object value) {
        try {
            return json.writeValueAsString(value);
        } catch (IOException ex) {
            throw error(ErrorCode.VALIDATION_ERROR);
        }
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"))
                .truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    }

    private static Instant instant(LocalDateTime time) {
        return time == null ? null : time.atZone(ZoneId.of("Asia/Shanghai")).toInstant();
    }

    private static class ExportFailure extends RuntimeException {
        final String reason;

        ExportFailure(String reason) {
            super(reason);
            this.reason = reason;
        }
    }
}
