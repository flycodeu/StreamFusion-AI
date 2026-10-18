package com.streamfusion.platform.system.access.query;

import java.time.Instant;
import java.util.List;

/** Read-only projections. They intentionally exclude contact details, credentials and sessions. */
public final class AccessQueryViews {
    private AccessQueryViews() {}

    public record Blocker(String code, String objectId, String name) {}

    public record DepartmentIdentity(String id, String name) {}

    public record UserIdentity(
            String id,
            String username,
            String nickname,
            int status,
            boolean mustChangePassword,
            boolean loginRestricted,
            boolean accountUsable,
            List<DepartmentIdentity> departments) {}

    public record RoleIdentity(String id, String code, String name, String status) {}

    public record PageInfo(
            String id,
            String name,
            String path,
            String menuPath,
            String moduleKey,
            boolean enabled,
            boolean chainEnabled,
            boolean navigationVisible,
            boolean moduleRegistered,
            List<Blocker> blockers) {}

    public record UserPage(
            PageInfo menu,
            boolean grantEffective,
            boolean accountUsable,
            boolean accessEligible,
            boolean moduleAccess,
            long sourceCount,
            List<RoleIdentity> sourcePreview,
            List<Blocker> blockers) {}

    public record PageUser(
            UserIdentity user,
            boolean grantEffective,
            boolean accessEligible,
            boolean moduleAccess,
            long sourceCount,
            List<RoleIdentity> sourcePreview,
            List<Blocker> blockers) {}

    public record PageRole(
            RoleIdentity role, boolean grantEffective, long userCount, List<Blocker> blockers) {}

    public record RoleOverview(
            RoleIdentity role,
            long assignedUserCount,
            long usableUserCount,
            long assignedPageCount,
            long effectivePageCount) {}

    public record RolePage(PageInfo menu, boolean grantEffective, List<Blocker> blockers) {}

    public record PageOverview(
            PageInfo menu,
            long assignedRoleCount,
            long activeRoleCount,
            long assignedUserCount,
            long eligibleUserCount) {}

    public record Result<T>(
            List<T> items,
            int page,
            int size,
            long total,
            Instant checkedAt,
            UserIdentity subjectUser,
            PageInfo subjectPage,
            PageUser subjectAccess) {}
}
