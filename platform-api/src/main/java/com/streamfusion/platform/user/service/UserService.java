package com.streamfusion.platform.user.service;

import com.baomidou.mybatisplus.spring.service.IService;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import java.time.LocalDateTime;

/** 用户数据操作，供认证和用户管理流程复用。权限及事务由业务流程负责。 */
public interface UserService extends IService<UserEntity> {
    /** 按大小写无关账号查找用户。 */
    UserEntity findByUsername(String username);

    /** 在调用方事务中锁定用户，锁持续到事务结束。 */
    UserEntity lockById(long id);

    /** Advances only the session version while the caller holds the user lock. */
    int replaceSession(long id, long expectedSessionVersion);

    /** Explicit administration action advances both session and edit versions. */
    int forceLogout(long id, long version, long actorId, LocalDateTime now);

    /** 核对密码及会话版本后更新失败次数和冷却时间。 */
    int updateLoginState(
            long id,
            String expectedHash,
            long expectedSessionVersion,
            int failedLoginCount,
            LocalDateTime lockedUntil,
            LocalDateTime now);

    /** 按编辑版本更新资料；可选字段允许清空。 */
    int updateProfile(
            long id,
            long version,
            String nickname,
            String avatarKey,
            String phone,
            String email,
            int gender,
            long actorId,
            LocalDateTime now);

    /** 核对旧密码及会话版本，原子完成改密和状态转换。 */
    int changePassword(
            long id,
            String expectedHash,
            long expectedSessionVersion,
            String newHash,
            LocalDateTime now);

    /** 按编辑版本删除用户。 */
    int hardDelete(long id, long version);

    /** 删除用户角色关联，须与用户删除处于同一事务。 */
    int deleteRoleBindings(long userId);

    /** 按编辑版本更新状态，同时使旧会话失效。 */
    int changeStatus(long id, long version, int newStatus, long actorId, LocalDateTime now);

    /** 原子重置密码；封禁用户保留封禁状态。 */
    int resetPassword(long id, long version, String newHash, long actorId, LocalDateTime now);
}
