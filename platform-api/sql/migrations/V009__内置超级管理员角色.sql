-- Only a protected role; never seed a user or a default password.
INSERT INTO sys_role(code, name, description)
VALUES ('SUPER_ADMIN', '超级管理员', '全部已注册功能权限，包含后续新增权限');
