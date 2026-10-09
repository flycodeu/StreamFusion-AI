package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.dto.CameraGroupWriteDto;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.camera.pojo.vo.*;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.*;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.*;
import org.springframework.web.context.request.*;

@Service
@RequiredArgsConstructor
public class CameraGroupService {
    public record CameraPlacement(long cameraId, long version, Long groupId, String lifecycle) {}

    private final CameraGroupMapper groups;
    private final CameraChannelMapper channels;
    private final CameraScopeMapper scopes;
    private final CameraAccessService access;
    private final CameraCryptoService crypto;
    private final AuditService audit;
    private final Clock clock;

    private CameraGroupTree tree() {
        return new CameraGroupTree(groups.allBounded());
    }

    public Map<Long, String> paths(Collection<Long> ids) {
        var tree = tree();
        Map<Long, String> result = new HashMap<>();
        for (Long id : ids) if (id != null) result.put(id, tree.path(id));
        return result;
    }

    public List<Long> queryGroupIds(long groupId, boolean descendants) {
        var tree = tree();
        if (tree.all().stream().noneMatch(g -> g.getId() == groupId)) return List.of();
        return descendants ? tree.descendants(List.of(groupId)) : List.of(groupId);
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
        var tree = tree();
        if (groupId != null) tree.require(groupId);
        Set<Long> users = new HashSet<>();
        if (groupId != null && newCameraCount > 0) {
            users.addAll(scopes.usersForGroups(tree.ancestors(groupId)));
            bounded(users.size());
            users.removeAll(access.superAdminIds(users));
        }
        Map<String, Object> result = new LinkedHashMap<>();
        result.put(
                "confirmation",
                confirmation(
                        "CAMERA_IMPORT", actor, jobId, version, groupId, selectionProof, tree));
        result.put("groupPath", groupId == null ? null : tree.path(groupId));
        result.put("affectedUserCount", users.size());
        return result;
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
        var tree = tree();
        if (groupId != null) tree.require(groupId);
        verify("CAMERA_IMPORT", actor, jobId, version, groupId, selectionProof, confirmation, tree);
    }

    /** Captures the approved placement boundary for background imports without HTTP context. */
    @Transactional(propagation = Propagation.MANDATORY)
    public String importScopeFingerprint(CameraAccessService.Actor actor, Long groupId) {
        return crypto.sign("camera-bulk-import-scope", importScopeState(actor, groupId));
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public boolean matchesImportScope(
            CameraAccessService.Actor actor, Long groupId, String fingerprint) {
        return crypto.verify(
                "camera-bulk-import-scope", importScopeState(actor, groupId), fingerprint);
    }

    private Object importScopeState(CameraAccessService.Actor actor, Long groupId) {
        access.requireSuper(actor);
        var tree = tree();
        if (groupId != null) tree.require(groupId);
        return List.of(actor.userId(), str(groupId), authorizationState(tree));
    }

    private List<Object> authorizationState(CameraGroupTree tree) {
        var revisions = scopes.scopeVersions();
        bounded(revisions.size());
        var authorization = scopes.authorizationRevisions();
        bounded(authorization.size());
        return List.of(
                tree.all().stream()
                        .map(g -> List.of(g.getId(), str(g.getParentId()), g.getVersion()))
                        .toList(),
                revisions.stream().map(s -> List.of(s.getUserId(), s.getVersion())).toList(),
                authorization);
    }

    @Transactional(readOnly = true)
    public PageResultVo<CameraGroupVo> page(String parentId, String name, int page, int size) {
        pageBounds(page, size);
        var actor = access.readActor();
        var tree = tree();
        Long parent = optionalId(parentId);
        String term = text(name, 64, false);
        var visibility = access.visibility(actor);
        Set<Long> visible = new HashSet<>();
        if (actor.superAdmin()) tree.all().forEach(g -> visible.add(g.getId()));
        else for (Long gid : scopes.visibleGroups(visibility)) visible.addAll(tree.ancestors(gid));
        List<CameraGroupEntity> rows =
                tree.all().stream()
                        .filter(g -> visible.contains(g.getId()))
                        .filter(
                                g ->
                                        term == null
                                                ? Objects.equals(parent, g.getParentId())
                                                : g.getName()
                                                        .toLowerCase(Locale.ROOT)
                                                        .contains(term.toLowerCase(Locale.ROOT)))
                        .toList();
        long offset = (long) (page - 1) * size;
        var counts = visibleCounts(tree, visibility);
        Instant observedAt = clock.instant();
        return new PageResultVo<>(
                rows.stream()
                        .skip(offset)
                        .limit(size)
                        .map(g -> vo(g, tree, visible, counts, observedAt))
                        .toList(),
                page,
                size,
                rows.size());
    }

    @Transactional(readOnly = true)
    public List<CameraGroupVo> visibleTree() {
        var actor = access.readActor();
        var tree = tree();
        var visibility = access.visibility(actor);
        Set<Long> visible = new HashSet<>();
        if (actor.superAdmin()) tree.all().forEach(g -> visible.add(g.getId()));
        else for (Long gid : scopes.visibleGroups(visibility)) visible.addAll(tree.ancestors(gid));
        var counts = visibleCounts(tree, visibility);
        var observedAt = clock.instant();
        return tree.all().stream()
                .filter(g -> visible.contains(g.getId()))
                .map(g -> vo(g, tree, visible, counts, observedAt))
                .toList();
    }

    @Transactional(readOnly = true)
    public CameraGroupVo get(String id) {
        var actor = access.readActor();
        access.requireSuper(actor);
        var tree = tree();
        return vo(
                tree.require(id(id)),
                tree,
                new HashSet<>(tree.all().stream().map(CameraGroupEntity::getId).toList()),
                visibleCounts(tree, access.visibility(actor)),
                clock.instant());
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraGroupVo create(CameraGroupWriteDto dto, AuditContextDto context) {
        if (dto == null) throw invalid();
        var actor = access.lockActor();
        access.requireSuper(actor);
        var tree = tree();
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
        var tree = tree();
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
            verify("GROUP_MOVE", actor, gid, version, parent, "", dto.getConfirmation(), tree);
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
        var tree = tree();
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
        var tree = tree();
        var group = tree.require(gid);
        if (group.getVersion() != ver) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        Long target = optionalId(targetParentId);
        tree.validateMove(gid, target);
        List<Long> affected = tree.descendants(List.of(gid));
        long count = scopes.visibleCount(affected, access.visibility(actor));
        return impact(
                null,
                group.getParentId(),
                target,
                "",
                tree.path(group.getParentId()),
                tree.path(target),
                count,
                groupImpact(tree, gid, target),
                confirmation("GROUP_MOVE", actor, gid, ver, target, "", tree));
    }

    @Transactional(readOnly = true)
    public List<CameraDeviceMovePreviewVo.Placement> devicePlacements(String groupKey) {
        access.requireSuper(access.readActor());
        return devicePlacements(deviceMembers(groupKey), tree());
    }

    private List<CameraDeviceMovePreviewVo.Placement> devicePlacements(
            List<CameraChannelEntity> members, CameraGroupTree tree) {
        Map<Long, Long> placements = new LinkedHashMap<>();
        for (var row : members)
            placements.merge(row.getGroupId() == null ? 0L : row.getGroupId(), 1L, Long::sum);
        return placements.entrySet().stream()
                .map(
                        e ->
                                new CameraDeviceMovePreviewVo.Placement(
                                        e.getKey() == 0 ? null : str(e.getKey()),
                                        e.getKey() == 0 ? "待归档" : tree.path(e.getKey()),
                                        e.getValue()))
                .toList();
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraDeviceMovePreviewVo deviceMovePreview(String groupKey, String targetGroupId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var members = deviceMembers(groupKey);
        long target = id(targetGroupId);
        var tree = tree();
        tree.require(target);
        for (var row : members) {
            validateTransition(row.getGroupId(), row.getLifecycle(), target, movedLifecycle(row));
        }
        var origins = devicePlacements(members, tree);
        String token =
                confirmation(
                        "DEVICE_MOVE",
                        actor,
                        id(groupKey.substring(1)),
                        0,
                        target,
                        membershipProof(groupKey, members),
                        tree);
        return new CameraDeviceMovePreviewVo(
                groupKey,
                origins,
                impact(
                        null,
                        null,
                        target,
                        "",
                        origins.size() == 1 ? origins.getFirst().groupPath() : "多个分组",
                        tree.path(target),
                        members.size(),
                        deviceImpact(members, tree, target),
                        token));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void moveDevice(
            String groupKey, String targetGroupId, String token, AuditContextDto context) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var members = deviceMembers(groupKey);
        long target = id(targetGroupId);
        var tree = tree();
        tree.require(target);
        verify(
                "DEVICE_MOVE",
                actor,
                id(groupKey.substring(1)),
                0,
                target,
                membershipProof(groupKey, members),
                token,
                tree);
        for (var row : members) {
            String state = movedLifecycle(row);
            validateTransition(row.getGroupId(), row.getLifecycle(), target, state);
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

    private List<CameraChannelEntity> deviceMembers(String groupKey) {
        if (groupKey == null || !groupKey.matches("[dc][1-9][0-9]{0,18}")) throw invalid();
        long key = id(groupKey.substring(1));
        var query = new LambdaQueryWrapper<CameraChannelEntity>();
        if (groupKey.charAt(0) == 'd') query.eq(CameraChannelEntity::getDeviceId, key);
        else query.eq(CameraChannelEntity::getId, key).isNull(CameraChannelEntity::getDeviceId);
        var rows =
                channels.selectList(
                        query.orderByAsc(CameraChannelEntity::getId).last("LIMIT 10001"));
        bounded(rows.size());
        if (rows.isEmpty()) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return rows;
    }

    private String membershipProof(String groupKey, List<CameraChannelEntity> rows) {
        return crypto.sign(
                "device-placement",
                List.of(
                        groupKey,
                        rows.stream()
                                .map(
                                        c ->
                                                List.of(
                                                        c.getId(),
                                                        c.getVersion(),
                                                        str(c.getGroupId()),
                                                        c.getLifecycle()))
                                .toList()));
    }

    private static String movedLifecycle(CameraChannelEntity row) {
        return "PENDING_ASSIGNMENT".equals(row.getLifecycle()) ? "ENABLED" : row.getLifecycle();
    }

    private Map<String, Object> deviceImpact(
            List<CameraChannelEntity> members, CameraGroupTree tree, long target) {
        Map<Long, Set<Long>> groupUsers = new HashMap<>(), directUsers = new HashMap<>();
        var inherited =
                scopes.groupGrantsForImpact(
                        tree.all().stream().map(CameraGroupEntity::getId).toList());
        bounded(inherited.size());
        for (var grant : inherited)
            groupUsers
                    .computeIfAbsent(grant.getTargetId(), x -> new HashSet<>())
                    .add(grant.getUserId());
        var direct =
                scopes.directGrantsForImpact(
                        members.stream().map(CameraChannelEntity::getId).toList());
        bounded(direct.size());
        for (var grant : direct)
            directUsers
                    .computeIfAbsent(grant.getTargetId(), x -> new HashSet<>())
                    .add(grant.getUserId());
        Set<Long> gained = new HashSet<>(), lost = new HashSet<>();
        for (var member : members) {
            var row = new CameraChannelScopeRow();
            row.setId(member.getId());
            row.setGroupId(member.getGroupId());
            Set<Long> before =
                    "ENABLED".equals(member.getLifecycle())
                            ? usersFromMaps(tree, row, groupUsers, directUsers)
                            : new HashSet<>();
            row.setGroupId(target);
            Set<Long> after =
                    "ENABLED".equals(movedLifecycle(member))
                            ? usersFromMaps(tree, row, groupUsers, directUsers)
                            : new HashSet<>();
            var plus = new HashSet<>(after);
            plus.removeAll(before);
            gained.addAll(plus);
            var minus = new HashSet<>(before);
            minus.removeAll(after);
            lost.addAll(minus);
        }
        var affected = new HashSet<>(gained);
        affected.addAll(lost);
        var admins = access.superAdminIds(affected);
        gained.removeAll(admins);
        lost.removeAll(admins);
        return Map.of(
                "gainedUserCount",
                gained.size(),
                "lostUserCount",
                lost.size(),
                "computedAt",
                clock.instant());
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraImpactVo cameraMovePreview(
            String cameraId, String cameraVersion, String targetGroupId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        long cid = id(cameraId);
        var row = requireChannel(cid);
        if (row.getVersion() != version(cameraVersion))
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        Long target = id(targetGroupId);
        var tree = tree();
        tree.require(target);
        String state =
                row.getLifecycle().equals("PENDING_ASSIGNMENT") ? "ENABLED" : row.getLifecycle();
        return cameraImpact(actor, row, target, state, tree);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraImpactVo cameraLifecyclePreview(
            String cameraId, String cameraVersion, String targetLifecycle) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var row = requireChannel(id(cameraId));
        if (row.getVersion() != version(cameraVersion))
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        validateTransition(row.getGroupId(), row.getLifecycle(), row.getGroupId(), targetLifecycle);
        return cameraImpact(actor, row, row.getGroupId(), targetLifecycle, tree());
    }

    /** Caller holds the shared coordination lock and owns the channel write transaction. */
    public void validateCameraChange(
            CameraAccessService.Actor actor,
            CameraPlacement current,
            Long targetGroupId,
            String targetLifecycle,
            String confirmation) {
        access.requireSuper(actor);
        var tree = tree();
        validateTransition(current.groupId(), current.lifecycle(), targetGroupId, targetLifecycle);
        tree.require(targetGroupId);
        if (Objects.equals(current.groupId(), targetGroupId)
                && current.lifecycle().equals(targetLifecycle)) return;
        verify(
                "CAMERA_CHANGE",
                actor,
                current.cameraId(),
                current.version(),
                targetGroupId,
                targetLifecycle,
                confirmation,
                tree);
    }

    private void validateTransition(Long oldGroup, String oldState, Long target, String state) {
        if (target == null || !("ENABLED".equals(state) || "DISABLED".equals(state)))
            throw invalid();
        if ("PENDING_ASSIGNMENT".equals(oldState) && (!"ENABLED".equals(state) || oldGroup != null))
            throw conflict();
    }

    private CameraImpactVo cameraImpact(
            CameraAccessService.Actor actor,
            CameraChannelScopeRow row,
            Long target,
            String state,
            CameraGroupTree tree) {
        validateTransition(row.getGroupId(), row.getLifecycle(), target, state);
        var before = usersForCamera(row, tree, row.getGroupId(), row.getLifecycle());
        var after = usersForCamera(row, tree, target, state);
        return impact(
                str(row.getId()),
                row.getGroupId(),
                target,
                state,
                tree.path(row.getGroupId()),
                tree.path(target),
                1,
                impactCounts(before, after),
                confirmation(
                        "CAMERA_CHANGE",
                        actor,
                        row.getId(),
                        row.getVersion(),
                        target,
                        state,
                        tree));
    }

    private Map<String, Object> groupImpact(CameraGroupTree tree, long moved, Long target) {
        var affected = scopes.channelsInGroups(tree.descendants(List.of(moved)));
        bounded(affected.size());
        var copies =
                tree.all().stream()
                        .map(
                                g -> {
                                    var c = new CameraGroupEntity();
                                    c.setId(g.getId());
                                    c.setParentId(g.getId() == moved ? target : g.getParentId());
                                    c.setName(g.getName());
                                    return c;
                                })
                        .toList();
        var afterTree = new CameraGroupTree(copies);
        Set<Long> gained = new HashSet<>(), lost = new HashSet<>();
        Map<Long, Set<Long>> groupUsers = new HashMap<>(), directUsers = new HashMap<>();
        var inherited =
                scopes.groupGrantsForImpact(
                        tree.all().stream().map(CameraGroupEntity::getId).toList());
        bounded(inherited.size());
        for (var grant : inherited) {
            groupUsers
                    .computeIfAbsent(grant.getTargetId(), x -> new HashSet<>())
                    .add(grant.getUserId());
        }
        var direct =
                scopes.directGrantsForImpact(
                        affected.stream().map(CameraChannelScopeRow::getId).toList());
        bounded(direct.size());
        for (var grant : direct)
            directUsers
                    .computeIfAbsent(grant.getTargetId(), x -> new HashSet<>())
                    .add(grant.getUserId());
        for (var camera : affected) {
            if (!"ENABLED".equals(camera.getLifecycle())) continue;
            Set<Long> before = usersFromMaps(tree, camera, groupUsers, directUsers);
            Set<Long> after = usersFromMaps(afterTree, camera, groupUsers, directUsers);
            Set<Long> plus = new HashSet<>(after);
            plus.removeAll(before);
            gained.addAll(plus);
            Set<Long> minus = new HashSet<>(before);
            minus.removeAll(after);
            lost.addAll(minus);
        }
        Set<Long> affectedUsers = new HashSet<>(gained);
        affectedUsers.addAll(lost);
        Set<Long> admins = access.superAdminIds(affectedUsers);
        gained.removeAll(admins);
        lost.removeAll(admins);
        return Map.of(
                "gainedUserCount",
                gained.size(),
                "lostUserCount",
                lost.size(),
                "computedAt",
                clock.instant());
    }

    private static Set<Long> usersFromMaps(
            CameraGroupTree tree,
            CameraChannelScopeRow c,
            Map<Long, Set<Long>> groups,
            Map<Long, Set<Long>> direct) {
        Set<Long> result = new HashSet<>(direct.getOrDefault(c.getId(), Set.of()));
        for (long gid : tree.ancestors(c.getGroupId()))
            result.addAll(groups.getOrDefault(gid, Set.of()));
        return result;
    }

    private Set<Long> usersForCamera(
            CameraChannelScopeRow row, CameraGroupTree tree, Long groupId, String state) {
        if (!"ENABLED".equals(state)) return new HashSet<>();
        var inherited = scopes.usersForGroups(tree.ancestors(groupId));
        var direct = scopes.usersForCamera(row.getId());
        bounded(inherited.size());
        bounded(direct.size());
        Set<Long> ids = new HashSet<>(inherited);
        ids.addAll(direct);
        ids.removeAll(access.superAdminIds(ids));
        return ids;
    }

    private Map<String, Object> impactCounts(Set<Long> before, Set<Long> after) {
        Set<Long> gained = new HashSet<>(after);
        gained.removeAll(before);
        Set<Long> lost = new HashSet<>(before);
        lost.removeAll(after);
        return Map.of(
                "gainedUserCount",
                gained.size(),
                "lostUserCount",
                lost.size(),
                "computedAt",
                clock.instant());
    }

    private String confirmation(
            String kind,
            CameraAccessService.Actor actor,
            long id,
            long version,
            Long target,
            String state,
            CameraGroupTree tree) {
        long expires = clock.instant().plusSeconds(300).getEpochSecond();
        return expires
                + "."
                + crypto.sign(
                        "camera-impact",
                        proof(kind, actor, id, version, target, state, expires, tree));
    }

    private void verify(
            String kind,
            CameraAccessService.Actor actor,
            long id,
            long version,
            Long target,
            String state,
            String token,
            CameraGroupTree tree) {
        if (token == null || token.length() > 4096) throw conflict();
        int dot = token.indexOf('.');
        if (dot < 1) throw conflict();
        long expires;
        try {
            expires = Long.parseLong(token.substring(0, dot));
        } catch (NumberFormatException e) {
            throw conflict();
        }
        long now = clock.instant().getEpochSecond();
        if (expires <= now
                || expires > now + 300
                || !crypto.verify(
                        "camera-impact",
                        proof(kind, actor, id, version, target, state, expires, tree),
                        token.substring(dot + 1))) throw conflict();
    }

    private Object proof(
            String kind,
            CameraAccessService.Actor actor,
            long id,
            long version,
            Long target,
            String state,
            long expires,
            CameraGroupTree tree) {
        var attrs = RequestContextHolder.getRequestAttributes();
        if (!(attrs instanceof ServletRequestAttributes servlet)
                || servlet.getRequest().getSession(false) == null)
            throw BusinessException.error(ErrorCode.UNAUTHORIZED);
        var authorizationState = authorizationState(tree);
        List<CameraChannelScopeRow> affected =
                "GROUP_MOVE".equals(kind)
                        ? scopes.channelsInGroups(tree.descendants(List.of(id)))
                        : List.of();
        bounded(affected.size());
        return List.of(
                kind,
                actor.userId(),
                servlet.getRequest().getSession(false).getId(),
                id,
                version,
                str(target),
                state,
                expires,
                authorizationState.get(0),
                authorizationState.get(1),
                authorizationState.get(2),
                affected.stream()
                        .map(
                                c ->
                                        List.of(
                                                c.getId(),
                                                c.getVersion(),
                                                str(c.getGroupId()),
                                                c.getLifecycle()))
                        .toList());
    }

    private CameraImpactVo impact(
            String cid,
            Long from,
            Long to,
            String state,
            String fromPath,
            String toPath,
            long count,
            Map<String, Object> auth,
            String token) {
        return new CameraImpactVo(
                cid,
                strOrNull(from),
                strOrNull(to),
                state,
                fromPath,
                toPath,
                count,
                auth,
                token,
                Instant.ofEpochSecond(Long.parseLong(token.substring(0, token.indexOf('.')))));
    }

    private CameraChannelScopeRow requireChannel(long id) {
        var row = scopes.channel(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    private Map<Long, Long> visibleCounts(
            CameraGroupTree tree, CameraAccessService.Visibility visibility) {
        Map<Long, Long> counts = new HashMap<>();
        for (var row : scopes.visibleGroupCounts(visibility))
            for (Long id : tree.ancestors(row.getGroupId()))
                counts.merge(id, row.getCameraCount(), Long::sum);
        return counts;
    }

    private CameraGroupVo vo(
            CameraGroupEntity row,
            CameraGroupTree tree,
            Set<Long> visible,
            Map<Long, Long> counts,
            Instant observedAt) {
        return new CameraGroupVo(
                str(row.getId()),
                strOrNull(row.getParentId()),
                row.getName(),
                row.getSortOrder(),
                row.getRemark(),
                str(row.getVersion()),
                tree.all().stream()
                        .anyMatch(
                                g ->
                                        Objects.equals(g.getParentId(), row.getId())
                                                && visible.contains(g.getId())),
                counts.getOrDefault(row.getId(), 0L),
                observedAt);
    }

    private void unique(Long parent, String name, Long exclude) {
        if (groups.sameName(parent, name, exclude) > 0) throw conflict();
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"));
    }

    static long id(String value) {
        return DecimalInput.id(value, "id");
    }

    static long version(String value) {
        return DecimalInput.version(value, "version");
    }

    static Long optionalId(String value) {
        return value == null ? null : id(value);
    }

    static String str(Object value) {
        return value == null ? "" : value.toString();
    }

    static String strOrNull(Object value) {
        return value == null ? null : value.toString();
    }

    static String text(String value, int max, boolean required) {
        String s = CameraSourceRules.text(value, max, required);
        return s == null || s.isEmpty() ? null : s;
    }

    static int order(Integer value) {
        if (value == null) return 0;
        if (value < 0) throw invalid();
        return value;
    }

    static void pageBounds(int page, int size) {
        if (page < 1 || size < 1 || size > 100) throw invalid();
    }

    static void bounded(int count) {
        if (count > 10000) throw conflict();
    }

    static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    static BusinessException conflict() {
        return BusinessException.error(ErrorCode.CONFLICT);
    }
}
