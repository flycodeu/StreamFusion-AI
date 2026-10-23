package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.storage.entity.StorageHistoryPolicy;
import com.streamfusion.platform.storage.mapper.StorageHistoryPolicyMapper;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class StorageHistoryPolicyService {
    private final StorageHistoryPolicyMapper policies;
    private final StorageConfigurationService configuration;
    private final StorageAccess access;

    public record Command(
            Long version, Boolean inheritGlobal, Boolean enabled, Integer retentionDays) {}

    public StorageHistoryPolicy lockGlobal() {
        return policies.selectOne(
                new LambdaQueryWrapper<StorageHistoryPolicy>()
                        .eq(StorageHistoryPolicy::getId, 0L)
                        .last("FOR UPDATE"));
    }

    public StorageHistoryPolicy effective(long instanceId, StorageHistoryPolicy global) {
        var own = policies.selectById(instanceId);
        return own == null || Boolean.TRUE.equals(own.getInheritGlobal()) ? global : own;
    }

    public Map<String, Object> detail(long id) {
        if (id != 0) configuration.require(id);
        var global = policies.selectById(0L);
        var own = id == 0 ? global : policies.selectById(id);
        boolean inherited = id != 0 && (own == null || Boolean.TRUE.equals(own.getInheritGlobal()));
        var effective = inherited ? global : own;
        var view = new LinkedHashMap<String, Object>();
        view.put("instanceId", id == 0 ? null : Long.toString(id));
        view.put("version", own == null ? "0" : own.getVersion().toString());
        view.put("inheritGlobal", inherited);
        view.put("enabled", own != null && Boolean.TRUE.equals(own.getEnabled()));
        view.put("retentionDays", own == null ? null : own.getRetentionDays());
        var applied = new LinkedHashMap<String, Object>();
        applied.put("enabled", effective != null && Boolean.TRUE.equals(effective.getEnabled()));
        applied.put("retentionDays", effective == null ? null : effective.getRetentionDays());
        applied.put("source", inherited || id == 0 ? "GLOBAL" : "INSTANCE");
        view.put("effective", applied);
        return view;
    }

    @Transactional
    public Map<String, Object> save(long id, Command input) {
        if (id != 0) configuration.lock(id);
        var global = lockGlobal();
        access.recheck(access.actor());
        if (input.version() == null
                || input.enabled() == null
                || input.inheritGlobal() == null
                || (id == 0 && input.inheritGlobal())
                || (input.retentionDays() != null
                        && (input.retentionDays() < 1 || input.retentionDays() > 3650))
                || (!input.inheritGlobal() && input.enabled() && input.retentionDays() == null))
            throw StorageRules.invalid();
        var row = id == 0 ? global : policies.selectById(id);
        StorageRules.version(input.version(), row == null ? 0L : row.getVersion());
        boolean create = row == null;
        if (create) {
            row = new StorageHistoryPolicy();
            row.setId(id);
        }
        row.setInheritGlobal(input.inheritGlobal());
        row.setEnabled(!input.inheritGlobal() && input.enabled());
        row.setRetentionDays(input.inheritGlobal() ? null : input.retentionDays());
        row.setVersion(input.version() + 1);
        row.setUpdatedBy(access.actor());
        row.setUpdatedAt(StorageRules.now());
        if (create) policies.insert(row);
        else policies.updateById(row);
        configuration.auditConfiguration(
                "STORAGE_HISTORY_POLICY",
                id == 0 ? null : id,
                "UPDATE",
                (id == 0 ? "全局" : configuration.require(id).getName())
                        + " / "
                        + (row.getInheritGlobal()
                                ? "继承全局"
                                : row.getEnabled() ? "保留" + row.getRetentionDays() + "天" : "关闭清理"));
        return detail(id);
    }
}
