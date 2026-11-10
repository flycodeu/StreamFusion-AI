query <<'SQL'
SELECT JSON_OBJECT('columns',JSON_ARRAY('账号','来源','认证方式','已锁定'),'total',(SELECT COUNT(*) FROM mysql.user),'rows',COALESCE(JSON_ARRAYAGG(JSON_OBJECT('账号',User,'来源',Host,'认证方式',plugin,'已锁定',IF(account_locked='Y','是','否'))),JSON_ARRAY())) FROM (SELECT User,Host,plugin,account_locked FROM mysql.user ORDER BY User,Host LIMIT 200) AS selected;
SQL
