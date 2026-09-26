package com.streamfusion.platform.user.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import java.time.LocalDateTime;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

/** 用户表基础映射与XML语句声明；普通查询和更新条件统一维护在UserServiceImpl。 */
@Mapper
public interface UserMapper extends BaseMapper<UserEntity> {
    /** 使用FOR UPDATE，在调用方事务内锁定用户。 */
    UserEntity lockById(@Param("id") long id);

    /** CASE状态转换及密码、会话版本校验在同一SQL内完成。 */
    int changePassword(
            @Param("id") long id,
            @Param("expectedHash") String expectedHash,
            @Param("expectedSessionVersion") long expectedSessionVersion,
            @Param("newHash") String newHash,
            @Param("now") LocalDateTime now);

    /** 清理关联表，不属于sys_user的通用CRUD。 */
    int deleteRoleBindings(@Param("userId") long userId);

    /** 重置密码并保留封禁状态，原子递增安全版本。 */
    int resetPassword(
            @Param("id") long id,
            @Param("version") long version,
            @Param("newHash") String newHash,
            @Param("actorId") long actorId,
            @Param("now") LocalDateTime now);
}
