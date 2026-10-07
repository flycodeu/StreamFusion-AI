package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.dto.CameraScopeUpdateDto;
import com.streamfusion.platform.camera.pojo.entity.*;
import com.streamfusion.platform.camera.pojo.vo.CameraScopeVo;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.*;
import com.streamfusion.platform.user.service.UserService;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.*;

@Service
@RequiredArgsConstructor
public class CameraScopeService {
    private final CameraScopeMapper scopes;
    private final CameraGroupMapper groups;
    private final CameraAccessService access;
    private final AccessService identities;
    private final UserService users;
    private final AuditService audit;
    private final Clock clock;

    @Transactional(readOnly = true, isolation = Isolation.REPEATABLE_READ)
    public CameraScopeVo get(String userId) {
        access.requireSuper(access.readActor());
        long uid = CameraGroupService.id(userId);
        if (users.getById(uid) == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return read(uid);
    }

    private CameraScopeVo read(long uid) {
        boolean admin = identities.isSuperAdmin(uid);
        var row = scopes.selectById(uid);
        var tree = new CameraGroupTree(groups.allBounded());
        var gids = scopes.grantedGroups(uid);
        var cids = scopes.grantedCameras(uid);
        if (gids.size() > 200 || cids.size() > 2000) throw CameraGroupService.conflict();
        var visibility = new CameraAccessService.Visibility(admin, uid, tree.descendants(gids));
        var groupViews =
                gids.stream()
                        .map(
                                id -> {
                                    var g = tree.require(id);
                                    return Map.<String, Object>of(
                                            "groupId",
                                            id.toString(),
                                            "name",
                                            g.getName(),
                                            "path",
                                            tree.path(id));
                                })
                        .toList();
        var cameraViews = scopes.channels(cids).stream().map(c -> cameraView(c, tree)).toList();
        return new CameraScopeVo(
                Long.toString(uid),
                row == null ? "0" : row.getVersion().toString(),
                admin ? "ALL" : "CUSTOM",
                gids.stream().map(Object::toString).toList(),
                cids.stream().map(Object::toString).toList(),
                groupViews,
                cameraViews,
                Map.of(
                        "effectiveCameraCount",
                        scopes.visibleCount(null, visibility),
                        "enabledCameraCount",
                        scopes.enabledCount(visibility),
                        "computedAt",
                        clock.instant()),
                row == null
                        ? null
                        : row.getUpdatedAt().atZone(ZoneId.of("Asia/Shanghai")).toInstant(),
                row == null ? null : CameraGroupService.strOrNull(row.getUpdatedBy()));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraScopeVo update(
            String userId, CameraScopeUpdateDto input, AuditContextDto context) {
        if (input == null) throw CameraGroupService.invalid();
        long uid = CameraGroupService.id(userId),
                expected = DecimalInput.version(input.version(), "version");
        var desiredGroups = ids(input.groupIds(), 200, "groupIds");
        var desiredCameras = ids(input.cameraIds(), 2000, "cameraIds");
        var actor = access.lockActor();
        access.requireSuper(actor);
        if (users.lockById(uid) == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (identities.isSuperAdmin(uid)) throw BusinessException.error(ErrorCode.FORBIDDEN);
        var row = scopes.selectById(uid);
        long current = row == null ? 0 : row.getVersion();
        if (expected != current) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        var tree = new CameraGroupTree(groups.allBounded());
        desiredGroups.forEach(tree::require);
        Set<Long> oldGroups = new HashSet<>(scopes.grantedGroups(uid)),
                oldCameras = new HashSet<>(scopes.grantedCameras(uid));
        var found = scopes.channels(desiredCameras);
        if (found.size() != desiredCameras.size()) throw CameraGroupService.invalid();
        for (var c : found)
            if (!oldCameras.contains(c.getId()) && !"ENABLED".equals(c.getLifecycle()))
                throw CameraGroupService.invalid();
        Set<Long> newGroups = new HashSet<>(desiredGroups),
                newCameras = new HashSet<>(desiredCameras);
        if (oldGroups.equals(newGroups) && oldCameras.equals(newCameras)) return read(uid);
        VersionCounter.requireIncrementable(current);
        LocalDateTime now = LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"));
        if (row == null) {
            row = new CameraUserScopeEntity();
            row.setUserId(uid);
            row.setVersion(0L);
            row.setCreatedAt(now);
            row.setUpdatedAt(now);
            row.setCreatedBy(actor.userId());
            row.setUpdatedBy(actor.userId());
            scopes.insert(row);
        }
        Set<Long> addedGroups = difference(newGroups, oldGroups),
                removedGroups = difference(oldGroups, newGroups),
                addedCameras = difference(newCameras, oldCameras),
                removedCameras = difference(oldCameras, newCameras);
        removedGroups.stream().sorted().forEach(g -> scopes.removeGroup(uid, g));
        removedCameras.stream().sorted().forEach(c -> scopes.removeCamera(uid, c));
        addedGroups.stream().sorted().forEach(g -> scopes.addGroup(uid, g, actor.userId(), now));
        addedCameras.stream().sorted().forEach(c -> scopes.addCamera(uid, c, actor.userId(), now));
        if (scopes.update(
                        null,
                        new LambdaUpdateWrapper<CameraUserScopeEntity>()
                                .eq(CameraUserScopeEntity::getUserId, uid)
                                .eq(CameraUserScopeEntity::getVersion, current)
                                .set(CameraUserScopeEntity::getVersion, current + 1)
                                .set(CameraUserScopeEntity::getUpdatedAt, now)
                                .set(CameraUserScopeEntity::getUpdatedBy, actor.userId()))
                != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(
                actor.userId(),
                "CAMERA_SCOPE",
                uid,
                "CAMERA_SCOPE_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "beforeVersion",
                        Long.toString(current),
                        "afterVersion",
                        Long.toString(current + 1),
                        "addedGroupCount",
                        Integer.toString(addedGroups.size()),
                        "removedGroupCount",
                        Integer.toString(removedGroups.size()),
                        "addedCameraCount",
                        Integer.toString(addedCameras.size()),
                        "removedCameraCount",
                        Integer.toString(removedCameras.size())));
        return read(uid);
    }

    @Transactional(readOnly = true)
    public PageResultVo<Map<String, Object>> userOptions(String name, int page, int size) {
        access.requireSuper(access.readActor());
        CameraGroupService.pageBounds(page, size);
        String term = like(name);
        var rows = scopes.userOptions(term, (long) (page - 1) * size, size);
        var adminIds = access.superAdminIds(rows.stream().map(u -> u.getId()).toList());
        var items =
                rows.stream()
                        .map(
                                u -> {
                                    Map<String, Object> item = new LinkedHashMap<>();
                                    item.put("userId", u.getId().toString());
                                    item.put("username", u.getUsername());
                                    item.put("nickname", u.getNickname());
                                    item.put("enabled", u.getStatus() != 2);
                                    item.put("isSuperAdmin", adminIds.contains(u.getId()));
                                    return item;
                                })
                        .toList();
        return new PageResultVo<>(items, page, size, scopes.userOptionCount(term));
    }

    @Transactional(readOnly = true)
    public PageResultVo<Map<String, Object>> cameraOptions(
            String groupId, boolean includeDescendants, String name, int page, int size) {
        access.requireSuper(access.readActor());
        CameraGroupService.pageBounds(page, size);
        Long gid = CameraGroupService.optionalId(groupId);
        String term = like(name);
        var tree = new CameraGroupTree(groups.allBounded());
        List<Long> groupIds =
                gid == null
                        ? null
                        : tree.all().stream().noneMatch(g -> Objects.equals(g.getId(), gid))
                                ? List.of()
                                : includeDescendants
                                        ? tree.descendants(List.of(gid))
                                        : List.of(gid);
        var rows = scopes.cameraOptions(term, groupIds, (long) (page - 1) * size, size);
        return new PageResultVo<>(
                rows.stream().map(c -> cameraView(c, tree)).toList(),
                page,
                size,
                scopes.cameraOptionCount(term, groupIds));
    }

    @Transactional(readOnly = true)
    public Map<String, Object> reasons(String userId, String cameraId) {
        access.requireSuper(access.readActor());
        long uid = CameraGroupService.id(userId), cid = CameraGroupService.id(cameraId);
        if (users.getById(uid) == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        var c = scopes.channel(cid);
        if (c == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        var tree = new CameraGroupTree(groups.allBounded());
        var ancestors = tree.ancestors(c.getGroupId());
        var grants =
                scopes.grantedGroups(uid).stream()
                        .filter(ancestors::contains)
                        .map(g -> Map.of("groupId", g.toString(), "path", tree.path(g)))
                        .toList();
        boolean direct = scopes.grantedCameras(uid).contains(cid);
        return Map.of(
                "cameraId",
                cameraId,
                "inScope",
                identities.isSuperAdmin(uid)
                        || (!"PENDING_ASSIGNMENT".equals(c.getLifecycle())
                                && (direct || !grants.isEmpty())),
                "direct",
                direct,
                "groups",
                grants,
                "computedAt",
                clock.instant());
    }

    private Map<String, Object> cameraView(CameraChannelScopeRow c, CameraGroupTree tree) {
        Map<String, Object> value = new LinkedHashMap<>();
        value.put("cameraId", c.getId().toString());
        value.put("name", c.getName());
        value.put("groupId", CameraGroupService.strOrNull(c.getGroupId()));
        value.put("path", tree.path(c.getGroupId()));
        value.put("groupPath", tree.path(c.getGroupId()));
        value.put("lifecycle", c.getLifecycle());
        return value;
    }

    private static List<Long> ids(List<String> raw, int limit, String field) {
        if (raw == null || raw.size() > limit) throw CameraGroupService.invalid();
        Set<Long> result = new TreeSet<>();
        for (String s : raw)
            if (!result.add(DecimalInput.id(s, field))) throw CameraGroupService.invalid();
        return List.copyOf(result);
    }

    private static Set<Long> difference(Set<Long> a, Set<Long> b) {
        Set<Long> result = new HashSet<>(a);
        result.removeAll(b);
        return result;
    }

    private static String like(String name) {
        String s = CameraGroupService.text(name, 128, false);
        return s == null ? null : s.replace("!", "!!").replace("%", "!%").replace("_", "!_");
    }
}
