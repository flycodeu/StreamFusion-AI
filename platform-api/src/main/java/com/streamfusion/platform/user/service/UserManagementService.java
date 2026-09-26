package com.streamfusion.platform.user.service;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.user.pojo.dto.UserCreateDto;
import com.streamfusion.platform.user.pojo.dto.UserProfileUpdateDto;
import com.streamfusion.platform.user.pojo.dto.UserQueryDto;
import com.streamfusion.platform.user.pojo.vo.UserSummaryVo;
import com.streamfusion.platform.user.pojo.vo.UserVo;

/** 用户管理流程入口，按实际角色权限关联授权；负责事务和返回对象，数据操作复用UserService。 */
public interface UserManagementService {
    PageResultVo<UserSummaryVo> page(UserQueryDto query);

    UserVo get(String id);

    UserVo create(UserCreateDto input, AuditContextDto context);

    UserVo update(String id, UserProfileUpdateDto input, AuditContextDto context);

    UserVo changeStatus(
            String id, String requestedVersion, boolean enabled, AuditContextDto context);

    void resetPassword(String id, String requestedVersion, AuditContextDto context);

    void delete(String id, String requestedVersion, AuditContextDto context);
}
