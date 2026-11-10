query <<'SQL'
SELECT JSON_OBJECT('columns',JSON_ARRAY('数据库','字符集','排序规则'),'total',(SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN ('mysql','sys','information_schema','performance_schema')),'rows',COALESCE(JSON_ARRAYAGG(JSON_OBJECT('数据库',SCHEMA_NAME,'字符集',DEFAULT_CHARACTER_SET_NAME,'排序规则',DEFAULT_COLLATION_NAME)),JSON_ARRAY())) FROM (SELECT * FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN ('mysql','sys','information_schema','performance_schema') ORDER BY SCHEMA_NAME LIMIT 200) AS selected;
SQL
