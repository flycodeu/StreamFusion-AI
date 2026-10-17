package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraGroupRules.*;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.camera.pojo.vo.*;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Request-local bounded snapshots and scope-aware projections; never mutates group state. */
@Component
@RequiredArgsConstructor
class CameraGroupQuery {
    private final CameraGroupMapper groups;
    private final CameraChannelMapper channels;
    private final CameraScopeMapper scopes;
    private final CameraAccessService access;
    private final Clock clock;

    CameraGroupTree snapshot() {
        return new CameraGroupTree(groups.allBounded());
    }

    Map<Long, String> paths(Collection<Long> ids) {
        var tree = snapshot();
        Map<Long, String> result = new HashMap<>();
        for (Long id : ids) if (id != null) result.put(id, tree.path(id));
        return result;
    }

    List<Long> queryGroupIds(long groupId, boolean descendants) {
        return queryGroupIds(List.of(groupId), descendants);
    }

    List<Long> queryGroupIds(Collection<Long> groupIds, boolean descendants) {
        if (groupIds.isEmpty()) return List.of();
        var tree = snapshot();
        var known = new HashSet<>(tree.all().stream().map(CameraGroupEntity::getId).toList());
        return groupIds.stream()
                .filter(known::contains)
                .flatMap(id -> (descendants ? tree.descendants(List.of(id)) : List.of(id)).stream())
                .distinct()
                .toList();
    }

    PageResultVo<CameraGroupVo> page(String parentId, String name, int page, int size) {
        pageBounds(page, size);
        var actor = access.readActor();
        var tree = snapshot();
        Long parent = optionalId(parentId);
        String term = text(name, 64, false);
        var visibility = access.visibility(actor, tree);
        var directCounts = scopes.visibleGroupCounts(visibility);
        Set<Long> visible = new HashSet<>();
        if (actor.superAdmin()) tree.all().forEach(g -> visible.add(g.getId()));
        else for (var count : directCounts) visible.addAll(tree.ancestors(count.getGroupId()));
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
        var counts = visibleCounts(tree, directCounts);
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

    List<CameraGroupVo> visibleTree() {
        var actor = access.readActor();
        var tree = snapshot();
        var visibility = access.visibility(actor, tree);
        var directCounts = scopes.visibleGroupCounts(visibility);
        Set<Long> visible = new HashSet<>();
        if (actor.superAdmin()) tree.all().forEach(g -> visible.add(g.getId()));
        else for (var count : directCounts) visible.addAll(tree.ancestors(count.getGroupId()));
        var counts = visibleCounts(tree, directCounts);
        var observedAt = clock.instant();
        return tree.all().stream()
                .filter(g -> visible.contains(g.getId()))
                .map(g -> vo(g, tree, visible, counts, observedAt))
                .toList();
    }

    CameraGroupVo get(String id) {
        var actor = access.readActor();
        access.requireSuper(actor);
        var tree = snapshot();
        return vo(
                tree.require(id(id)),
                tree,
                new HashSet<>(tree.all().stream().map(CameraGroupEntity::getId).toList()),
                visibleCounts(tree, access.visibility(actor)),
                clock.instant());
    }

    List<CameraDeviceMovePreviewVo.Placement> devicePlacements(String groupKey) {
        access.requireSuper(access.readActor());
        return devicePlacements(deviceMembers(groupKey), snapshot());
    }

    List<CameraDeviceMovePreviewVo.Placement> devicePlacements(
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

    List<CameraChannelEntity> deviceMembers(String groupKey) {
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

    CameraChannelScopeRow requireChannel(long id) {
        var row = scopes.channel(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    private Map<Long, Long> visibleCounts(
            CameraGroupTree tree, CameraAccessService.Visibility visibility) {
        return visibleCounts(tree, scopes.visibleGroupCounts(visibility));
    }

    private Map<Long, Long> visibleCounts(
            CameraGroupTree tree, List<CameraGroupCountRow> directCounts) {
        Map<Long, Long> counts = new HashMap<>();
        for (var row : directCounts)
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
}
