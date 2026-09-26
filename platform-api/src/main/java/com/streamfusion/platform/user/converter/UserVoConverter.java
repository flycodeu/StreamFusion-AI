package com.streamfusion.platform.user.converter;

import com.streamfusion.platform.access.pojo.vo.RoleVo;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.vo.DepartmentVo;
import com.streamfusion.platform.user.pojo.vo.UserProfileVo;
import com.streamfusion.platform.user.pojo.vo.UserSummaryVo;
import com.streamfusion.platform.user.pojo.vo.UserVo;
import java.util.List;
import org.springframework.stereotype.Component;

/** 按接口用途选择用户字段，不把数据库实体或安全字段传到前端。 */
@Component
public class UserVoConverter {
    public UserVo detail(UserEntity user, List<RoleVo> roles, List<DepartmentVo> departments) {
        return new UserVo(
                user.getId().toString(),
                user.getUsername(),
                user.getNickname(),
                user.getAvatarKey(),
                user.getPhone(),
                user.getEmail(),
                user.getGender(),
                user.getStatus(),
                Boolean.TRUE.equals(user.getMustChangePassword()),
                List.copyOf(departments),
                List.copyOf(roles),
                user.getVersion().toString());
    }

    public UserSummaryVo summary(
            UserEntity user, List<RoleVo> roles, List<DepartmentVo> departments) {
        return new UserSummaryVo(
                user.getId().toString(),
                user.getUsername(),
                user.getNickname(),
                user.getAvatarKey(),
                user.getStatus(),
                List.copyOf(departments),
                List.copyOf(roles),
                user.getVersion().toString());
    }

    public UserProfileVo profile(UserEntity user) {
        return new UserProfileVo(
                user.getId().toString(),
                user.getUsername(),
                user.getNickname(),
                user.getAvatarKey(),
                user.getPhone(),
                user.getEmail(),
                user.getGender(),
                user.getStatus(),
                Boolean.TRUE.equals(user.getMustChangePassword()),
                user.getVersion().toString());
    }
}
