#!/bin/sh
# Установка xray-selector на Keenetic/Netcraze с XKeen (Entware)
set -e
cd "$(dirname "$0")"

# Удаление: ./install.sh --uninstall — убирает только панель (XKeen и конфиги Xray не трогает)
if [ "$1" = "--uninstall" ]; then
  [ -x /opt/etc/init.d/S99xray-selector ] && /opt/etc/init.d/S99xray-selector stop >/dev/null 2>&1 || true
  rm -f /opt/etc/init.d/S99xray-selector /opt/sbin/xray-selector
  rm -rf /opt/share/xray-selector
  echo "Панель удалена. XKeen, конфиги Xray и файлы настроек (/opt/etc/xray-selector*.json) оставлены."
  echo "Если они больше не нужны: rm -rf /opt/etc/xray-selector*.json /opt/etc/xray-selector-backups"
  exit 0
fi

case "$(uname -m)" in
  aarch64|arm64) BIN=xray-selector-arm64 ;;
  mips)          BIN=xray-selector-mips ;;
  mipsel|mipsle) BIN=xray-selector-mipsle ;;
  *) echo "Неизвестная архитектура: $(uname -m)"; exit 1 ;;
esac
echo "Архитектура: $(uname -m) → $BIN"

[ -x /opt/etc/init.d/S99xray-selector ] && /opt/etc/init.d/S99xray-selector stop >/dev/null 2>&1 || true
cp "$BIN" /opt/sbin/xray-selector
chmod 755 /opt/sbin/xray-selector
mkdir -p /opt/share/xray-selector && cp "$0" /opt/share/xray-selector/install.sh 2>/dev/null || cp install.sh /opt/share/xray-selector/install.sh
chmod 755 /opt/share/xray-selector/install.sh
# адрес роутера в домашней сети (br0), чтобы панель не была видна из интернета
LAN_IP=$(ip -4 addr show br0 2>/dev/null | awk '/inet /{sub(/\/.*/,"",$2);print $2;exit}')
[ -z "$LAN_IP" ] && LAN_IP=$(ip -4 route 2>/dev/null | awk '/br0/ && /src/ {for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')
[ -z "$LAN_IP" ] && LAN_IP=192.168.1.1
PORT=9091
if [ -f /opt/etc/init.d/S99xray-selector ]; then # при обновлении — оставить прежний адрес
  OLD=$(sed -n 's/.*-listen \([0-9.]*\):\([0-9]*\).*/\1:\2/p' /opt/etc/init.d/S99xray-selector | head -1)
  [ -n "$OLD" ] && LAN_IP=${OLD%:*} && PORT=${OLD#*:}
fi
sed "s#-listen [0-9.]*:[0-9]*#-listen $LAN_IP:$PORT#" S99xray-selector > /opt/etc/init.d/S99xray-selector
chmod 755 /opt/etc/init.d/S99xray-selector
mkdir -p /opt/var/log

CONF=/opt/etc/xray/configs
NEED_RESTART=0
if ! grep -qs '"api"' "$CONF"/*.json; then
  cat > "$CONF/07_api.json" <<'JSON'
{
  "api": {
    "tag": "api",
    "listen": "127.0.0.1:10085",
    "services": ["RoutingService", "ObservatoryService"]
  }
}
JSON
  echo "Создан $CONF/07_api.json (Xray API на 127.0.0.1:10085)"
  NEED_RESTART=1
else
  echo "Секция api уже есть в конфигах — проверьте, что в services есть RoutingService и ObservatoryService, а listen = 127.0.0.1:10085"
fi

if [ "$NEED_RESTART" = 1 ]; then
  echo "Перезапускаю XKeen…"
  xkeen -restart || echo "Не удалось перезапустить — выполните: xkeen -restart"
fi

/opt/etc/init.d/S99xray-selector start
sleep 1
echo
echo "Готово. Панель: http://$LAN_IP:$PORT"
echo "Пароль на панель необязателен — его можно задать в Настройки → Пароль."
echo "Удалить панель: sh /opt/share/xray-selector/install.sh --uninstall"
echo "Журнал: /opt/var/log/xray-selector.log"
