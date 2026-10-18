-- UX-OPS-DESIGN-01: menu data only. Back up and verify in a stopped-write copy first.
-- MySQL 8.0.17+; do not run the DROP/CREATE initialization on an existing database.
-- The new PAGE follows ordinary delegation. Initial SUPER_ADMIN seed is not a super-only guard.
SET NAMES utf8mb4;
SET time_zone = '+08:00';
DELIMITER $$
CREATE PROCEDURE sf_add_access_query_page()
BEGIN
    DECLARE system_id BIGINT;
    DECLARE super_id BIGINT;
    DECLARE page_id BIGINT;
    DECLARE EXIT HANDLER FOR SQLEXCEPTION
    BEGIN
        ROLLBACK;
        RESIGNAL;
    END;
    START TRANSACTION;
    SELECT id INTO super_id FROM sys_role WHERE code='SUPER_ADMIN' AND status='ENABLED' FOR UPDATE;
    IF super_id IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Enabled SUPER_ADMIN role required'; END IF;
    SELECT parent_id INTO system_id FROM sys_menu WHERE path='/system/users' AND type='PAGE';
    IF system_id IS NULL OR NOT EXISTS(SELECT 1 FROM sys_menu WHERE id=system_id AND type='DIRECTORY') THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Verify the existing system directory before upgrading';
    END IF;
    SELECT id INTO page_id FROM sys_menu WHERE route_name='SystemAccessQuery';
    IF page_id IS NULL THEN
        IF EXISTS(SELECT 1 FROM sys_menu WHERE id=1012 OR path='/system/access-query') THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Menu ID or path conflict: manual review required';
        END IF;
        INSERT INTO sys_menu(id,parent_id,name,type,route_name,path,component_key,module_key,icon,sort_order,visible,enabled)
        VALUES(1012,system_id,'权限查询','PAGE','SystemAccessQuery','/system/access-query','/system/AccessQuery','access-query','role',4,TRUE,TRUE);
        SET page_id=1012;
    ELSEIF NOT EXISTS(SELECT 1 FROM sys_menu WHERE id=page_id AND type='PAGE' AND path='/system/access-query'
        AND component_key='/system/AccessQuery' AND module_key='access-query' AND parent_id=system_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Existing permission query menu differs: no overwrite';
    END IF;
    INSERT INTO sys_role_menu(role_id,menu_id)
    SELECT super_id,page_id WHERE NOT EXISTS(SELECT 1 FROM sys_role_menu WHERE role_id=super_id AND menu_id=page_id);
    COMMIT;
END$$
DELIMITER ;
CALL sf_add_access_query_page();
DROP PROCEDURE sf_add_access_query_page;
-- On failure inspect the transaction and remove this exact helper procedure before retrying.
-- Application rollback can retain the menu data; disable this PAGE if the old app cannot resolve it.
