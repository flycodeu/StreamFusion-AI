package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraGroupRules.*;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.dto.CameraGroupWriteDto;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.camera.pojo.vo.*;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.*;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.*;

@Service
@RequiredArgsConstructor
public class CameraGroupService {
    public record CameraPlacement(long cameraId, long version, Long groupId, String lifecycle) {}

    private final CameraGroupMapper groups;
    private final CameraChannelMapper channels;
    private final CameraAccessService access;
    private final CameraGroupQuery queries;
    private final CameraGroupChangePlanner changes;
    private final AuditService audit;
    private final Clock clock;

    public Map<Long, String> paths(Collection<Long> ids) {
        return queries.paths(ids);
    }

    public List<Long> queryGroupIds(long groupId, boolean descendants) {
        return queries.queryGroupIds(groupId, descendants);
    }

    /** A plan may select multiple roots; load their common tree once, preserving root order. */
    public List<Long> queryGroupIds(Collection<Long> groupIds, boolean descendants) {
        return queries.queryGroupIds(groupIds, descendants);
    }

    /** Import confirmation reuses exactly the tree, scope and identity revision proof of moves. */
    @Transactional(propagation = Propagation.MANDATORY)
    public Map<String, Object> previewImport(
            CameraAccessService.Actor actor,
            long jobId,
            long version,
            Long groupId,
            int newCameraCount,
            String selectionProof) {
        access.requireSuper(actor);
        var tree = queries.snapshot();
        return changes.previewImport(
                actor, jobId, version, groupId, newCameraCount, selectionProof, tree);
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public void verifyImport(
            CameraAccessService.Actor actor,
            long jobId,
            long version,
            Long groupId,
            String selectionProof,
            String confirmation) {
        access.requireSuper(actor);
        var tree = queries.snapshot();
        if (groupId != null) tree.require(groupId);
        changes.verify(
                "CAMERA_IMPORT",
                actor,
                jobId,
                version,
                groupId,
                selectionProof,
                confirmation,
                tree);
    }

    /** Captures the approved placement boundary for background imports without HTTP context. */
    @Transactional(propagation = Propagation.MANDATORY)
    public String importScopeFingerprint(CameraAccessService.Actor actor, Long groupId) {
        access.requireSuper(actor);
        var tree = queries.snapshot();
        if (groupId != null) tree.require(groupId);
        return changes.importScopeFingerprint(actor, groupId, tree);
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public boolean matchesImportScope(
            CameraAccessService.Actor actor, Long groupId, String fingerprint) {
        access.requireSuper(actor);
        var tree = queries.snapshot();
        if (groupId != null) tree.require(groupId);
        return changes.matchesImportScope(actor, groupId, fingerprint, tree);
    }

    @Transactional(readOnly = true)
    public PageResultVo<CameraGroupVo> page(String parentId, String name, int page, int size) {
        return queries.page(parentId, name, page, size);
    }

    @Transactional(readOnly = true)
    public List<CameraGroupVo> visibleTree() {
        return queries.visibleTree();
    }

    @Transactional(readOnly = true)
    public CameraGroupVo get(String id) {
        return queries.get(id);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraGroupVo create(CameraGroupWriteDto dto, AuditContextDto context) {
        if (dto == null) throw invalid();
        var actor = access.lockActor();
        access.requireSuper(actor);
        var tree = queries.snapshot();
        if (tree.all().size() >= CameraGroupTree.MAX_NODES) throw conflict();
        String name = text(dto.getName(), 64, true);
        Long parent = optionalId(dto.getParentId());
        int sort = order(dto.getSortOrder());
        tree.validateMove(null, parent);
        unique(parent, name, null);
        var row = new CameraGroupEntity();
        row.setName(name);
        row.setParentId(parent);
        row.setSortOrder(sort);
        row.setRemark(text(dto.getRemark(), 500, false));
        row.setVersion(0L);
        row.setCreatedAt(now());
        row.setUpdatedAt(row.getCreatedAt());
        row.setCreatedBy(actor.userId());
        row.setUpdatedBy(actor.userId());
        groups.insert(row);
        audit.record(
                actor.userId(),
                "CAMERA_GROUP",
                row.getId(),
                "CAMERA_GROUP_CREATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", name, "parentId", str(parent)));
        return get(str(row.getId()));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraGroupVo update(String id, CameraGroupWriteDto dto, AuditContextDto context) {
        if (dto == null) throw invalid();
        var actor = access.lockActor();
        access.requireSuper(actor);
        long gid = id(id);
        var tree = queries.snapshot();
        var current = tree.require(gid);
        long version = version(dto.getVersion());
        if (version != current.getVersion())
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        Long parent =
                dto.isParentIdProvided() ? optionalId(dto.getParentId()) : current.getParentId();
        String name = dto.getName() == null ? current.getName() : text(dto.getName(), 64, true);
        String remark =
                dto.isRemarkProvided() ? text(dto.getRemark(), 500, false) : current.getRemark();
        int sort = dto.getSortOrder() == null ? current.getSortOrder() : order(dto.getSortOrder());
        tree.validateMove(gid, parent);
        unique(parent, name, gid);
        if (!Objects.equals(parent, current.getParentId()))
            changes.verify(
                    "GROUP_MOVE", actor, gid, version, parent, "", dto.getConfirmation(), tree);
        if (Objects.equals(parent, current.getParentId())
                && name.equals(current.getName())
                && Objects.equals(remark, current.getRemark())
                && sort == current.getSortOrder()) return get(id);
        VersionCounter.requireIncrementable(version);
        int changed =
                groups.update(
                        null,
                        new LambdaUpdateWrapper<CameraGroupEntity>()
                                .eq(CameraGroupEntity::getId, gid)
                                .eq(CameraGroupEntity::getVersion, version)
                                .set(CameraGroupEntity::getParentId, parent)
                                .set(CameraGroupEntity::getName, name)
                                .set(CameraGroupEntity::getRemark, remark)
                                .set(CameraGroupEntity::getSortOrder, sort)
                                .set(CameraGroupEntity::getVersion, version + 1)
                                .set(CameraGroupEntity::getUpdatedAt, now())
                                .set(CameraGroupEntity::getUpdatedBy, actor.userId()));
        if (changed != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(
                actor.userId(),
                "CAMERA_GROUP",
                gid,
                "CAMERA_GROUP_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "name",
                        name,
                        "parentId",
                        str(parent),
                        "beforeVersion",
                        str(version),
                        "afterVersion",
                        str(version + 1)));
        return get(id);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String id, String requestedVersion, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        long gid = id(id);
        var tree = queries.snapshot();
        var row = tree.require(gid);
        long version = version(requestedVersion);
        if (version != row.getVersion())
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        if (tree.all().stream().anyMatch(g -> Objects.equals(g.getParentId(), gid))
                || groups.cameras(gid) > 0
                || groups.grants(gid) > 0) throw conflict();
        if (groups.delete(
                        new LambdaQueryWrapper<CameraGroupEntity>()
                                .eq(CameraGroupEntity::getId, gid)
                                .eq(CameraGroupEntity::getVersion, version))
                != 1) throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        audit.record(
                actor.userId(),
                "CAMERA_GROUP",
                gid,
                "CAMERA_GROUP_DELETE",
                "SUCCESS",
                null,
                context,
                Map.of("name", row.getName()));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraImpactVo movePreview(String id, String version, String targetParentId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        long gid = id(id), ver = version(version);
        var tree = queries.snapshot();
        var group = tree.require(gid);
        if (group.getVersion() != ver) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        Long target = optionalId(targetParentId);
        tree.validateMove(gid, target);
        return changes.groupMovePreview(actor, group, target, tree);
    }

    @Transactional(readOnly = true)
    public List<CameraDeviceMovePreviewVo.Placement> devicePlacements(String groupKey) {
        return queries.devicePlacements(groupKey);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraDeviceMovePreviewVo deviceMovePreview(String groupKey, String targetGroupId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var members = queries.deviceMembers(groupKey);
        long target = id(targetGroupId);
        var tree = queries.snapshot();
        tree.require(target);
        for (var row : members) {
            changes.validateTransition(
                    row.getGroupId(),
                    row.getLifecycle(),
                    target,
                    CameraGroupChangePlanner.movedLifecycle(row));
        }
        var origins = queries.devicePlacements(members, tree);
        String token =
                changes.confirmation(
                        "DEVICE_MOVE",
                        actor,
                        id(groupKey.substring(1)),
                        0,
                        target,
                        changes.membershipProof(groupKey, members),
                        tree);
        return new CameraDeviceMovePreviewVo(
                groupKey,
                origins,
                changes.impact(
                        null,
                        null,
                        target,
                        "",
                        origins.size() == 1 ? origins.getFirst().groupPath() : "多个分组",
                        tree.path(target),
                        members.size(),
                        changes.deviceImpact(members, tree, target),
                        token));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void moveDevice(
            String groupKey, String targetGroupId, String token, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var members = queries.deviceMembers(groupKey);
        long target = id(targetGroupId);
        var tree = queries.snapshot();
        tree.require(target);
        changes.verify(
                "DEVICE_MOVE",
                actor,
                id(groupKey.substring(1)),
                0,
                target,
                changes.membershipProof(groupKey, members),
                token,
                tree);
        for (var row : members) {
            String state = CameraGroupChangePlanner.movedLifecycle(row);
            changes.validateTransition(row.getGroupId(), row.getLifecycle(), target, state);
            if (Objects.equals(row.getGroupId(), target) && row.getLifecycle().equals(state))
                continue;
            VersionCounter.requireIncrementable(row.getVersion());
            int changed =
                    channels.update(
                            null,
                            new LambdaUpdateWrapper<CameraChannelEntity>()
                                    .eq(CameraChannelEntity::getId, row.getId())
                                    .eq(CameraChannelEntity::getVersion, row.getVersion())
                                    .set(CameraChannelEntity::getGroupId, target)
                                    .set(CameraChannelEntity::getLifecycle, state)
                                    .set(CameraChannelEntity::getVersion, row.getVersion() + 1)
                                    .set(CameraChannelEntity::getUpdatedAt, now())
                                    .set(CameraChannelEntity::getUpdatedBy, actor.userId()));
            if (changed != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        audit.record(
                actor.userId(),
                groupKey.startsWith("d") ? "CAMERA_DEVICE" : "CAMERA",
                id(groupKey.substring(1)),
                "CAMERA_DEVICE_MOVE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "code",
                        groupKey,
                        "afterGroupId",
                        str(target),
                        "cameraIdsSummary",
                        members.size() + " 个通道"));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraImpactVo cameraMovePreview(
            String cameraId, String cameraVersion, String targetGroupId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        long cid = id(cameraId);
        var row = queries.requireChannel(cid);
        if (row.getVersion() != version(cameraVersion))
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        Long target = id(targetGroupId);
        var tree = queries.snapshot();
        tree.require(target);
        String state =
                row.getLifecycle().equals("PENDING_ASSIGNMENT") ? "ENABLED" : row.getLifecycle();
        return changes.cameraImpact(actor, row, target, state, tree);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraImpactVo cameraLifecyclePreview(
            String cameraId, String cameraVersion, String targetLifecycle) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var row = queries.requireChannel(id(cameraId));
        if (row.getVersion() != version(cameraVersion))
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        changes.validateTransition(
                row.getGroupId(), row.getLifecycle(), row.getGroupId(), targetLifecycle);
        return changes.cameraImpact(
                actor, row, row.getGroupId(), targetLifecycle, queries.snapshot());
    }

    /** Caller holds the shared coordination lock and owns the channel write transaction. */
    public void validateCameraChange(
            CameraAccessService.Actor actor,
            CameraPlacement current,
            Long targetGroupId,
            String targetLifecycle,
            String confirmation) {
        access.requireSuper(actor);
        var tree = queries.snapshot();
        changes.validateTransition(
                current.groupId(), current.lifecycle(), targetGroupId, targetLifecycle);
        tree.require(targetGroupId);
        if (Objects.equals(current.groupId(), targetGroupId)
                && current.lifecycle().equals(targetLifecycle)) return;
        changes.verify(
                "CAMERA_CHANGE",
                actor,
                current.cameraId(),
                current.version(),
                targetGroupId,
                targetLifecycle,
                confirmation,
                tree);
    }

    private void unique(Long parent, String name, Long exclude) {
        if (groups.sameName(parent, name, exclude) > 0) throw conflict();
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"));
    }
}
