package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraGroupRules.*;

import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraImpactVo;
import com.streamfusion.platform.common.exception.*;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;
import org.springframework.web.context.request.*;

/** Computes scope impact and binds confirmation to the caller's unchanged tree snapshot. */
@Component
@RequiredArgsConstructor
class CameraGroupChangePlanner {
    private final CameraScopeMapper scopes;
    private final CameraAccessService access;
    private final CameraCryptoService crypto;
    private final Clock clock;

    String importScopeFingerprint(
            CameraAccessService.Actor actor, Long groupId, CameraGroupTree tree) {
        return crypto.sign("camera-bulk-import-scope", importScopeState(actor, groupId, tree));
    }

    boolean matchesImportScope(
            CameraAccessService.Actor actor,
            Long groupId,
            String fingerprint,
            CameraGroupTree tree) {
        return crypto.verify(
                "camera-bulk-import-scope", importScopeState(actor, groupId, tree), fingerprint);
    }

    private Object importScopeState(
            CameraAccessService.Actor actor, Long groupId, CameraGroupTree tree) {
        return List.of(actor.userId(), str(groupId), authorizationState(tree));
    }

    CameraImpactVo groupMovePreview(
            CameraAccessService.Actor actor,
            CameraGroupEntity group,
            Long target,
            CameraGroupTree tree) {
        List<Long> affected = tree.descendants(List.of(group.getId()));
        long count = scopes.visibleCount(affected, access.visibility(actor));
        return impact(
                null,
                group.getParentId(),
                target,
                "",
                tree.path(group.getParentId()),
                tree.path(target),
                count,
                groupImpact(tree, group.getId(), target),
                confirmation(
                        "GROUP_MOVE", actor, group.getId(), group.getVersion(), target, "", tree));
    }

    Map<String, Object> previewImport(
            CameraAccessService.Actor actor,
            long jobId,
            long version,
            Long groupId,
            int newCameraCount,
            String selectionProof,
            CameraGroupTree tree) {
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

    String membershipProof(String groupKey, List<CameraChannelEntity> rows) {
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

    static String movedLifecycle(CameraChannelEntity row) {
        return "PENDING_ASSIGNMENT".equals(row.getLifecycle()) ? "ENABLED" : row.getLifecycle();
    }

    Map<String, Object> deviceImpact(
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
        Set<Long> memberGroups = new HashSet<>();
        for (var member : members)
            if (!"PENDING_ASSIGNMENT".equals(member.getLifecycle()) && member.getGroupId() != null)
                memberGroups.add(member.getGroupId());
        for (var member : members) {
            var row = new CameraChannelScopeRow();
            row.setId(member.getId());
            row.setGroupId(member.getGroupId());
            Set<Long> before =
                    "ENABLED".equals(member.getLifecycle())
                            ? usersFromMaps(tree, row, groupUsers, directUsers, memberGroups)
                            : new HashSet<>();
            row.setGroupId(target);
            Set<Long> after =
                    "ENABLED".equals(movedLifecycle(member))
                            ? usersFromMaps(tree, row, groupUsers, directUsers, Set.of(target))
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

    void validateTransition(Long oldGroup, String oldState, Long target, String state) {
        if (target == null || !("ENABLED".equals(state) || "DISABLED".equals(state)))
            throw invalid();
        if ("PENDING_ASSIGNMENT".equals(oldState) && (!"ENABLED".equals(state) || oldGroup != null))
            throw conflict();
    }

    CameraImpactVo cameraImpact(
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
        Map<String, Set<Long>> deviceGroups = new HashMap<>();
        for (var camera : affected) {
            if ("PENDING_ASSIGNMENT".equals(camera.getLifecycle()) || camera.getGroupId() == null)
                continue;
            deviceGroups
                    .computeIfAbsent(deviceKey(camera), key -> new HashSet<>())
                    .add(camera.getGroupId());
        }
        Set<String> visitedDevices = new HashSet<>();
        for (var camera : affected) {
            if (!"ENABLED".equals(camera.getLifecycle())) continue;
            if (!visitedDevices.add(deviceKey(camera))) continue;
            var memberGroups = deviceGroups.getOrDefault(deviceKey(camera), Set.of());
            Set<Long> before = usersFromMaps(tree, camera, groupUsers, directUsers, memberGroups);
            Set<Long> after =
                    usersFromMaps(afterTree, camera, groupUsers, directUsers, memberGroups);
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
            Map<Long, Set<Long>> direct,
            Set<Long> memberGroups) {
        Set<Long> result = new HashSet<>(direct.getOrDefault(c.getId(), Set.of()));
        for (long memberGroup : memberGroups)
            for (long gid : tree.ancestors(memberGroup))
                result.addAll(groups.getOrDefault(gid, Set.of()));
        return result;
    }

    private static String deviceKey(CameraChannelScopeRow row) {
        return row.getDeviceId() == null ? "c" + row.getId() : "d" + row.getDeviceId();
    }

    private Set<Long> usersForCamera(
            CameraChannelScopeRow row, CameraGroupTree tree, Long groupId, String state) {
        var members = scopes.deviceMembersForImpact(row.getId());
        bounded(members.size());
        if (members.stream()
                .noneMatch(
                        member ->
                                "ENABLED"
                                        .equals(
                                                Objects.equals(member.getId(), row.getId())
                                                        ? state
                                                        : member.getLifecycle())))
            return new HashSet<>();
        Set<Long> ancestors = new HashSet<>();
        // The current channel may move; its sibling placements keep their independent group links.
        for (var member : members) {
            Long placement =
                    Objects.equals(member.getId(), row.getId()) ? groupId : member.getGroupId();
            String lifecycle =
                    Objects.equals(member.getId(), row.getId()) ? state : member.getLifecycle();
            if (!"PENDING_ASSIGNMENT".equals(lifecycle))
                ancestors.addAll(tree.ancestors(placement));
        }
        var inherited = scopes.usersForGroups(List.copyOf(ancestors));
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

    String confirmation(
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

    void verify(
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
                        : "CAMERA_CHANGE".equals(kind)
                                ? scopes.deviceMembersForImpact(id)
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

    CameraImpactVo impact(
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
}
