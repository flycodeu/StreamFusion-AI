package com.streamfusion.platform.system.access.query;

import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** Minimal mapper rows, separate from API objects and full identity entities. */
public final class AccessQueryRows {
    private AccessQueryRows() {}

    @Getter
    @Setter
    public static class UserRow {
        private Long id;
        private String username;
        private String nickname;
        private Integer status;
        private Boolean mustChangePassword;
        private LocalDateTime lockedUntil;
        private Long sourceCount;
        private Long activeSourceCount;
    }

    @Getter
    @Setter
    public static class RoleRow {
        private Long id;
        private String code;
        private String name;
        private String status;
        private Long userCount;
        private Long usableUserCount;
        private Long pageCount;
    }

    @Getter
    @Setter
    public static class GrantRow {
        private Long menuId;
        private Long sourceCount;
        private Long activeSourceCount;
    }

    @Getter
    @Setter
    public static class SourceRow extends RoleRow {
        private Long subjectId;
    }

    @Getter
    @Setter
    public static class PageCountsRow {
        private Long menuId;
        private Long roleCount;
        private Long activeRoleCount;
        private Long userCount;
        private Long eligibleUserCount;
    }

    @Getter
    @Setter
    public static class ExportAssignmentRow extends UserRow {
        private Long menuId;
    }
}
