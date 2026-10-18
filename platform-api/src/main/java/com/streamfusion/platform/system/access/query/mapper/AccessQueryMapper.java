package com.streamfusion.platform.system.access.query.mapper;

import com.streamfusion.platform.system.access.query.AccessQueryRows;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface AccessQueryMapper {
    AccessQueryRows.UserRow user(@Param("id") long id);

    List<AccessQueryRows.UserRow> users(
            @Param("keyword") String keyword,
            @Param("offset") long offset,
            @Param("size") int size);

    long userCount(@Param("keyword") String keyword);

    List<AccessQueryRows.RoleRow> roles(
            @Param("keyword") String keyword,
            @Param("offset") long offset,
            @Param("size") int size);

    long roleCount(@Param("keyword") String keyword);

    AccessQueryRows.RoleRow role(@Param("id") long id);

    List<AccessQueryRows.UserRow> roleUsers(
            @Param("roleId") long roleId,
            @Param("keyword") String keyword,
            @Param("offset") long offset,
            @Param("size") int size);

    long roleUserCount(@Param("roleId") long roleId, @Param("keyword") String keyword);

    List<Long> rolePageIds(@Param("roleId") long roleId);

    List<AccessQueryRows.PageCountsRow> pageCounts(@Param("menuIds") List<Long> menuIds);

    List<AccessQueryRows.ExportAssignmentRow> exportAssignments(
            @Param("menuIds") List<Long> menuIds);

    List<AccessQueryRows.GrantRow> userGrants(@Param("userId") long userId);

    List<AccessQueryRows.UserRow> pageUsers(
            @Param("menuId") long menuId,
            @Param("userId") Long userId,
            @Param("keyword") String keyword,
            @Param("includeInactive") boolean includeInactive,
            @Param("offset") long offset,
            @Param("size") int size);

    long pageUserCount(
            @Param("menuId") long menuId,
            @Param("userId") Long userId,
            @Param("keyword") String keyword,
            @Param("includeInactive") boolean includeInactive);

    List<AccessQueryRows.RoleRow> pageRoles(
            @Param("menuId") long menuId,
            @Param("userId") Long userId,
            @Param("keyword") String keyword,
            @Param("includeInactive") boolean includeInactive,
            @Param("offset") long offset,
            @Param("size") int size);

    long pageRoleCount(
            @Param("menuId") long menuId,
            @Param("userId") Long userId,
            @Param("keyword") String keyword,
            @Param("includeInactive") boolean includeInactive);

    List<AccessQueryRows.SourceRow> userPageSources(
            @Param("userId") long userId, @Param("menuIds") List<Long> menuIds);

    List<AccessQueryRows.SourceRow> pageUserSources(
            @Param("menuId") long menuId, @Param("userIds") List<Long> userIds);

    List<Long> moduleUsers(
            @Param("moduleKey") String moduleKey, @Param("userIds") List<Long> userIds);
}
