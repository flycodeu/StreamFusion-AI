package com.streamfusion.platform.user.service.impl;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.spring.service.impl.ServiceImpl;
import com.streamfusion.platform.common.validation.VersionCounter;
import com.streamfusion.platform.user.mapper.UserMapper;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserService;
import java.time.LocalDateTime;
import org.springframework.stereotype.Service;

/** 用户数据操作；查询和更新条件在此维护，事务及权限由调用方的业务流程控制。 */
@Service
public class UserServiceImpl extends ServiceImpl<UserMapper, UserEntity> implements UserService {
    public UserServiceImpl(UserMapper mapper) {
        this.baseMapper = mapper;
    }

    @Override
    public UserEntity findByUsername(String username) {
        return getOne(new LambdaQueryWrapper<UserEntity>().eq(UserEntity::getUsername, username));
    }

    @Override
    public int replaceSession(long id, long expectedSessionVersion) {
        return baseMapper.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getSessionVersion, expectedSessionVersion)
                        .set(
                                UserEntity::getSessionVersion,
                                VersionCounter.next(expectedSessionVersion)));
    }

    @Override
    public int forceLogout(long id, long version, long actorId, LocalDateTime now) {
        UserEntity observed = getById(id);
        if (observed == null || observed.getVersion() != version) return 0;
        return baseMapper.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getVersion, version)
                        .eq(UserEntity::getSessionVersion, observed.getSessionVersion())
                        .set(
                                UserEntity::getSessionVersion,
                                VersionCounter.next(observed.getSessionVersion()))
                        .set(UserEntity::getVersion, VersionCounter.next(version))
                        .set(UserEntity::getUpdatedBy, actorId)
                        .set(UserEntity::getUpdatedAt, now));
    }

    @Override
    public int updateLoginState(
            long id,
            String expectedHash,
            long expectedSessionVersion,
            int failedLoginCount,
            LocalDateTime lockedUntil,
            LocalDateTime now) {
        return baseMapper.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getPassword, expectedHash)
                        .eq(UserEntity::getSessionVersion, expectedSessionVersion)
                        .in(
                                UserEntity::getStatus,
                                UserStatus.PENDING_PASSWORD.getCode(),
                                UserStatus.NORMAL.getCode())
                        .set(UserEntity::getFailedLoginCount, failedLoginCount)
                        .set(UserEntity::getLockedUntil, lockedUntil)
                        .set(UserEntity::getUpdatedAt, now));
    }

    @Override
    public int updateProfile(
            long id,
            long version,
            String nickname,
            String avatarKey,
            String phone,
            String email,
            int gender,
            long actorId,
            LocalDateTime now) {
        return baseMapper.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getVersion, version)
                        .set(UserEntity::getNickname, nickname)
                        .set(UserEntity::getAvatarKey, avatarKey)
                        .set(UserEntity::getPhone, phone)
                        .set(UserEntity::getEmail, email)
                        .set(UserEntity::getGender, gender)
                        .set(UserEntity::getUpdatedBy, actorId)
                        .set(UserEntity::getUpdatedAt, now)
                        .set(UserEntity::getVersion, VersionCounter.next(version)));
    }

    @Override
    public int hardDelete(long id, long version) {
        return baseMapper.delete(
                new LambdaQueryWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getVersion, version));
    }

    @Override
    public int changeStatus(long id, long version, int newStatus, long actorId, LocalDateTime now) {
        UserEntity observed = getById(id);
        if (observed == null || observed.getVersion() != version) return 0;
        long nextSession = VersionCounter.next(observed.getSessionVersion());
        long nextVersion = VersionCounter.next(version);
        return baseMapper.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getVersion, version)
                        .set(UserEntity::getStatus, newStatus)
                        .eq(UserEntity::getSessionVersion, observed.getSessionVersion())
                        .set(UserEntity::getSessionVersion, nextSession)
                        .set(UserEntity::getVersion, nextVersion)
                        .set(UserEntity::getUpdatedBy, actorId)
                        .set(UserEntity::getUpdatedAt, now));
    }

    @Override
    public UserEntity lockById(long id) {
        return baseMapper.lockById(id);
    }

    @Override
    public int changePassword(
            long id,
            String expectedHash,
            long expectedSessionVersion,
            String newHash,
            LocalDateTime now) {
        UserEntity observed = getById(id);
        if (observed == null || observed.getSessionVersion() != expectedSessionVersion) return 0;
        VersionCounter.requireIncrementable(observed.getVersion());
        VersionCounter.requireIncrementable(observed.getSessionVersion());
        return baseMapper.changePassword(id, expectedHash, expectedSessionVersion, newHash, now);
    }

    @Override
    public int resetPassword(
            long id, long version, String newHash, long actorId, LocalDateTime now) {
        UserEntity observed = getById(id);
        if (observed == null || observed.getVersion() != version) return 0;
        VersionCounter.requireIncrementable(observed.getVersion());
        VersionCounter.requireIncrementable(observed.getSessionVersion());
        return baseMapper.resetPassword(id, version, newHash, actorId, now);
    }

    @Override
    public int deleteRoleBindings(long userId) {
        return baseMapper.deleteRoleBindings(userId);
    }
}
