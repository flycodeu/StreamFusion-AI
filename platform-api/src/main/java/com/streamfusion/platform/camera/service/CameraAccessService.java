package com.streamfusion.platform.camera.service;

import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class CameraAccessService {
    public record Actor(long userId, boolean superAdmin) {}

    public record Visibility(boolean superAdmin, long userId, List<Long> groupIds) {}

    private final AccessMapper coordination;
    private final AccessService access;
    private final CurrentUserService current;
    private final ModuleAuthorizationService authorization;
    private final CameraGroupMapper groups;
    private final CameraScopeMapper scopes;

    public Actor readActor() {
        var user = current.requireNormal();
        authorization.recheckCurrentRequest(user.getId());
        return new Actor(user.getId(), access.isSuperAdmin(user.getId()));
    }

    public Actor lockActor() {
        coordination.lockSuperAdminRole();
        return readActor();
    }

    public void requireSuper(Actor actor) {
        if (!actor.superAdmin()) throw BusinessException.error(ErrorCode.FORBIDDEN);
    }

    public Visibility visibility(Actor actor) {
        return new Visibility(
                actor.superAdmin(),
                actor.userId(),
                actor.superAdmin()
                        ? List.of()
                        : new CameraGroupTree(groups.allBounded())
                                .descendants(scopes.grantedGroups(actor.userId())));
    }

    public void requireVisible(long cameraId, Actor actor) {
        if (scopes.visibleCamera(cameraId, visibility(actor)) != 1)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
    }

    /** Reuses the identity domain's role predicate in bounded batches, never one query per user. */
    public Set<Long> superAdminIds(Collection<Long> userIds) {
        List<Long> ids = userIds.stream().distinct().sorted().toList();
        CameraGroupService.bounded(ids.size());
        Set<Long> result = new HashSet<>();
        for (int offset = 0; offset < ids.size(); offset += 1000)
            for (var role :
                    coordination.findRolesForUsers(
                            ids.subList(offset, Math.min(offset + 1000, ids.size()))))
                if ("SUPER_ADMIN".equals(role.getCode())) result.add(role.getUserId());
        return result;
    }
}
