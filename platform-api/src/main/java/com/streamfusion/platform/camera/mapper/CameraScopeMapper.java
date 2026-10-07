package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.*;
import com.streamfusion.platform.camera.service.CameraAccessService.Visibility;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.*;

@Mapper
public interface CameraScopeMapper extends BaseMapper<CameraUserScopeEntity> {
    List<Long> grantedGroups(@Param("userId") long userId);

    List<Long> grantedCameras(@Param("userId") long userId);

    CameraChannelScopeRow channel(@Param("id") long id);

    long visibleCamera(@Param("id") long id, @Param("visibility") Visibility visibility);

    List<Long> visibleGroups(@Param("visibility") Visibility visibility);

    List<CameraGroupCountRow> visibleGroupCounts(@Param("visibility") Visibility visibility);

    long visibleCount(
            @Param("groupIds") List<Long> groups, @Param("visibility") Visibility visibility);

    long enabledCount(@Param("visibility") Visibility visibility);

    List<CameraChannelScopeRow> channels(@Param("ids") List<Long> ids);

    List<UserEntity> userOptions(
            @Param("name") String name, @Param("offset") long offset, @Param("size") int size);

    long userOptionCount(@Param("name") String name);

    List<CameraChannelScopeRow> cameraOptions(
            @Param("name") String name,
            @Param("groupIds") List<Long> groupIds,
            @Param("offset") long offset,
            @Param("size") int size);

    long cameraOptionCount(@Param("name") String name, @Param("groupIds") List<Long> groupIds);

    List<CameraUserScopeEntity> scopeVersions();

    List<String> authorizationRevisions();

    List<Long> usersForGroups(@Param("groups") List<Long> groups);

    List<Long> usersForCamera(@Param("cameraId") long cameraId);

    List<CameraChannelScopeRow> channelsInGroups(@Param("groups") List<Long> groups);

    List<CameraGrantRow> groupGrantsForImpact(@Param("groups") List<Long> groups);

    List<CameraGrantRow> directGrantsForImpact(@Param("cameras") List<Long> cameras);

    int addGroup(
            @Param("userId") long userId,
            @Param("groupId") long groupId,
            @Param("actorId") long actorId,
            @Param("now") LocalDateTime now);

    int removeGroup(@Param("userId") long userId, @Param("groupId") long groupId);

    int addCamera(
            @Param("userId") long userId,
            @Param("cameraId") long cameraId,
            @Param("actorId") long actorId,
            @Param("now") LocalDateTime now);

    int removeCamera(@Param("userId") long userId, @Param("cameraId") long cameraId);
}
