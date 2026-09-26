package com.streamfusion.platform.department.service;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentTreeQueryDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentUpdateDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentWriteDto;
import com.streamfusion.platform.department.pojo.dto.UserDepartmentsUpdateDto;
import com.streamfusion.platform.department.pojo.vo.DepartmentNodeVo;
import com.streamfusion.platform.department.pojo.vo.DepartmentOptionVo;
import com.streamfusion.platform.department.pojo.vo.UserDepartmentsVo;
import com.streamfusion.platform.user.pojo.vo.DepartmentVo;
import java.util.List;
import java.util.Map;

public interface DepartmentService {
    List<DepartmentNodeVo> tree(DepartmentTreeQueryDto query);

    List<DepartmentOptionVo> options();

    DepartmentNodeVo create(DepartmentWriteDto input, AuditContextDto context);

    DepartmentNodeVo update(String id, DepartmentUpdateDto input, AuditContextDto context);

    void delete(String id, String version, AuditContextDto context);

    UserDepartmentsVo userDepartments(String userId);

    UserDepartmentsVo assign(
            String userId, UserDepartmentsUpdateDto input, AuditContextDto context);

    /**
     * Shares the create/update transaction; the user workflow owns its single version increment.
     */
    void assignForUserWrite(long userId, List<String> departmentIds, AuditContextDto context);

    Map<Long, List<DepartmentVo>> forUsers(List<Long> userIds);

    void removeUserBindings(long userId);
}
