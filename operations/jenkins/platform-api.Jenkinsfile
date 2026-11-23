pipeline {
    agent { label 'linux-jdk21' }

    options {
        skipDefaultCheckout()
        disableConcurrentBuilds()
        timeout(time: 60, unit: 'MINUTES')
        buildDiscarder(logRotator(numToKeepStr: '20', artifactNumToKeepStr: '10'))
    }

    stages {
        stage('拉取源码') {
            steps {
                echo 'SFOPS_STAGE: 拉取源码'
                dir('source') {
                    deleteDir()
                    checkout scm
                    sh '''
                        set -eu
                        printf '工作区 %s\\n' "$PWD"
                        printf 'Git 提交 '
                        git rev-parse HEAD
                    '''
                }
            }
        }

        stage('环境预检') {
            steps {
                echo 'SFOPS_STAGE: 环境预检'
                sh '''
                    set -eu
                    git --version
                    java -version
                    javac -version
                    java_major=$(java -XshowSettings:properties -version 2>&1 | sed -n 's/^[[:space:]]*java.specification.version = //p')
                    compiler_major=$(javac -version 2>&1 | sed -n 's/^javac \\([0-9]*\\).*/\\1/p')
                    if [ "$java_major" != 21 ] || [ "$compiler_major" != 21 ]; then
                        echo '构建节点需要 JDK 21，请检查 PATH 和 JAVA_HOME。' >&2
                        exit 1
                    fi
                    command -v sha256sum
                    command -v jar
                '''
            }
        }

        stage('编译与测试') {
            steps {
                echo 'SFOPS_STAGE: 编译与测试'
                dir('source/platform-api') {
                    sh '''
                        set -eu
                        version_output=$(LC_ALL=C sh mvnw -B -ntp -Dstyle.color=never -v)
                        printf '%s\\n' "$version_output"
                        maven_java=$(printf '%s\\n' "$version_output" | sed -n 's/^Java version: \\([0-9]*\\)[., ].*/\\1/p')
                        if [ "$maven_java" != 21 ]; then
                            echo 'Maven Wrapper 实际使用的 Java 不是 JDK 21，请检查 JAVA_HOME。' >&2
                            exit 1
                        fi
                        sh mvnw -B -ntp test
                    '''
                }
            }
        }

        stage('打包与校验') {
            steps {
                echo 'SFOPS_STAGE: 打包与校验'
                dir('source/platform-api') {
                    sh 'sh mvnw -B -ntp -DskipTests verify'
                }
            }
        }

        stage('归档 JAR') {
            steps {
                echo 'SFOPS_STAGE: 归档 JAR'
                dir('source/platform-api') {
                    sh '''
                        set -eu
                        primary=$(find target -maxdepth 1 -type f -name 'platform-api-*.jar' ! -name '*-preview-node.jar')
                        if [ "$(printf '%s\\n' "$primary" | grep -c .)" -ne 1 ] || [ ! -s "$primary" ]; then
                            echo '应当生成且仅生成一个 platform-api 主应用 JAR。' >&2
                            exit 1
                        fi
                        jar tf "$primary" | awk '
                            /^BOOT-INF\\/classes\\// { classes = 1 }
                            /^BOOT-INF\\/lib\\// { libs = 1 }
                            END { exit !(classes && libs) }
                        '
                        sha256sum "$primary"
                    '''
                    archiveArtifacts artifacts: 'target/platform-api-*.jar', excludes: 'target/*-preview-node.jar', allowEmptyArchive: false,
                        fingerprint: true, followSymlinks: false, onlyIfSuccessful: true
                }
            }
        }
    }
}
