package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.mapper.*;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** User-owned metadata retention. Never calls S3 or changes current observations/statistics. */
@Service
@RequiredArgsConstructor
public class StorageHistoryService {
    private static final int BATCH_SIZE = 100;
    private static final List<String> TERMINAL =
            List.of("SUCCEEDED", "PARTIAL", "FAILED", "CANCELLED", "UNKNOWN");
    private final StorageTaskMapper tasks;
    private final StorageCleanupPlanMapper plans;
    private final StorageCleanupItemMapper items;
    private final PlatformTransactionManager transactions;
    private final StorageHistoryPolicyService policies;
    // Restarting only rechecks records; eligibility always comes from the locked policy.
    // Scanning by ID prevents disabled instances or referenced records starving other instances.
    private long afterId;

    public synchronized void prune() {
        var candidates =
                tasks.selectList(
                        new LambdaQueryWrapper<StorageTask>()
                                .in(StorageTask::getStatus, TERMINAL)
                                .lt(StorageTask::getFinishedAt, StorageRules.now().minusDays(1))
                                .gt(StorageTask::getId, afterId)
                                .orderByAsc(StorageTask::getId)
                                .last("LIMIT " + BATCH_SIZE));
        for (var candidate : candidates) {
            new TransactionTemplate(transactions)
                    .executeWithoutResult(
                            status -> {
                                var policy =
                                        policies.effective(
                                                candidate.getInstanceId(), policies.lockGlobal());
                                if (policy == null
                                        || !Boolean.TRUE.equals(policy.getEnabled())
                                        || policy.getRetentionDays() == null) return;
                                var cutoff =
                                        StorageRules.now().minusDays(policy.getRetentionDays());
                                var current = tasks.selectById(candidate.getId());
                                if (current == null
                                        || !TERMINAL.contains(current.getStatus())
                                        || current.getFinishedAt() == null
                                        || !current.getFinishedAt().isBefore(cutoff)) return;
                                if (plans.selectCount(
                                                new LambdaQueryWrapper<StorageCleanupPlan>()
                                                        .eq(
                                                                StorageCleanupPlan
                                                                        ::getExecutionTaskId,
                                                                current.getId()))
                                        > 0) return;
                                var plan = plans.selectById(current.getId());
                                if (plan != null) {
                                    if (plan.getExpiresAt().isAfter(StorageRules.now())) return;
                                    if (plan.getExecutionTaskId() != null) {
                                        var execution = tasks.selectById(plan.getExecutionTaskId());
                                        if (execution != null
                                                && (!TERMINAL.contains(execution.getStatus())
                                                        || execution.getFinishedAt() == null
                                                        || !execution
                                                                .getFinishedAt()
                                                                .isBefore(cutoff))) return;
                                    }
                                }
                                items.delete(
                                        new LambdaQueryWrapper<StorageCleanupItem>()
                                                .eq(
                                                        StorageCleanupItem::getPlanId,
                                                        current.getId()));
                                plans.deleteById(current.getId());
                                tasks.deleteById(current.getId());
                            });
            afterId = candidate.getId();
        }
        if (candidates.size() < BATCH_SIZE) afterId = 0;
    }
}
