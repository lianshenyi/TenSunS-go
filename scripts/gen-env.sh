#!/usr/bin/env sh
# 生成 .env：为敏感项随机生成密码 / Token，其余配置沿用 .env.example。
#
#   ./scripts/gen-env.sh            # 生成 .env（已存在则不覆盖）
#   ./scripts/gen-env.sh --force    # 覆盖已存在的 .env
#
# 随机生成的三项：
#   consul_management_token  Consul ACL 引导 Token（UUID v4）
#   consul_token             后端 X-Consul-Token，必须与上面完全一致
#   admin_passwd             内置 admin 账号登录密码
#
# 其余键（端口、绑定地址、镜像名、日志级别等）保持 .env.example 的默认值。
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
EXAMPLE="$ROOT/.env.example"
ENV_FILE="$ROOT/.env"
FORCE=0

for arg in "$@"; do
    case "$arg" in
        --force|-f) FORCE=1 ;;
        -h|--help)
            sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "gen-env: unknown argument: $arg" >&2
            exit 2
            ;;
    esac
done

if [ ! -f "$EXAMPLE" ]; then
    echo "gen-env: missing template: $EXAMPLE" >&2
    exit 1
fi

if [ -f "$ENV_FILE" ] && [ "$FORCE" -ne 1 ]; then
    echo "gen-env: .env already exists, not overwritten (use --force to regenerate)"
    exit 0
fi

# 随机 UUID：优先用 uuidgen，缺失时回退到 /dev/urandom。
random_uuid() {
    if command -v uuidgen >/dev/null 2>&1; then
        uuidgen
        return 0
    fi
    if [ -r /dev/urandom ]; then
        # 取 16 字节随机数，套用 RFC 4122 v4 的版本位/变体位布局。
        od -An -tx1 -N16 /dev/urandom | tr -d ' \n' | awk '
            {
                hex = $0
                hex = substr(hex, 1, 12) "4" substr(hex, 14, 3) "a" substr(hex, 18, 3)
                printf "%s-%s-%s-%s-%s\n",
                    substr(hex, 1, 8), substr(hex, 9, 4), substr(hex, 13, 4),
                    substr(hex, 17, 4), substr(hex, 21, 12)
            }'
        return 0
    fi
    echo "gen-env: no way to generate random values (uuidgen and /dev/urandom missing)" >&2
    exit 1
}

# 随机十六进制串：用于 admin 密码。
random_hex() {
    random_uuid | tr -d '-' | cut -c1-"$1"
}

CONSUL_TOKEN="$(random_uuid)"
ADMIN_PASSWD="$(random_hex 24)"

# 用 awk 逐行替换占位值，其余内容原样保留。
TMP_FILE="$ENV_FILE.tmp.$$"
trap 'rm -f "$TMP_FILE"' EXIT INT TERM

awk -v token="$CONSUL_TOKEN" -v passwd="$ADMIN_PASSWD" '
    /^[[:space:]]*consul_management_token[[:space:]]*=/ {
        print "consul_management_token=" token
        next
    }
    /^[[:space:]]*consul_token[[:space:]]*=/ {
        print "consul_token=" token
        next
    }
    /^[[:space:]]*admin_passwd[[:space:]]*=/ {
        print "admin_passwd=" passwd
        next
    }
    { print }
' "$EXAMPLE" > "$TMP_FILE"

mv "$TMP_FILE" "$ENV_FILE"
chmod 600 "$ENV_FILE"
trap - EXIT INT TERM

echo "gen-env: wrote $ENV_FILE"
echo "gen-env:   consul_management_token / consul_token = $CONSUL_TOKEN"
echo "gen-env:   admin_passwd = $ADMIN_PASSWD"
echo "gen-env: 请妥善保存 .env（权限 600，已 git-ignore），不要提交到仓库。"
