literal() { local value=$1; printf "'%s'" "${value//\'/\'\'}"; }
