#!/bin/sh
# Установка xray-selector на Keenetic/Netcraze с XKeen (Entware)
set -e
cd "$(dirname "$0")"

# Удаление: ./install.sh --uninstall — убирает только панель (XKeen и конфиги Xray не трогает)
if [ "$1" = "--uninstall" ]; then
  [ -x /opt/etc/init.d/S99xray-selector ] && /opt/etc/init.d/S99xray-selector stop >/dev/null 2>&1 || true
  # убрать то, что панель добавила в конфиги Xray (07_api.json — только если его создала панель)
  CONF=/opt/etc/xray/configs
  if [ -f /opt/share/xray-selector/created.list ]; then
    while read -r f; do [ -n "$f" ] && rm -f "$f" && echo "Удалён $f"; true; done < /opt/share/xray-selector/created.list
  fi
  if [ -f "$CONF/08_stats.json" ]; then rm -f "$CONF/08_stats.json"; echo "Удалён $CONF/08_stats.json"; fi
  # policy со статистикой без секции stats не даст Xray запуститься — вернуть копию
  if grep -qs statsOutbound "$CONF"/*.json && ! grep -qs '"stats"' "$CONF"/*.json; then
    for f in /opt/etc/xray-selector-*.json.bak; do
      [ -f "$f" ] || continue
      n=${f#/opt/etc/xray-selector-}; n=${n%.bak}
      grep -qs statsOutbound "$CONF/$n" && cp "$f" "$CONF/$n" && echo "Восстановлен $CONF/$n из резервной копии"; true
    done
  fi
  rm -f /opt/etc/init.d/S99xray-selector /opt/sbin/xray-selector
  rm -rf /opt/share/xray-selector
  xkeen -restart >/dev/null 2>&1 && echo "XKeen перезапущен" || echo "Перезапустите XKeen: xkeen -restart"
  echo "Панель удалена, её добавки в конфиги Xray убраны. XKeen и ваши серверы не тронуты."
  echo "Если они больше не нужны: rm -rf /opt/etc/xray-selector*.json /opt/etc/xray-selector-backups"
  exit 0
fi

CONF=/opt/etc/xray/configs
NOBAL=0
grep -qs '"balancers"' "$CONF"/*.json || NOBAL=1

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
  # ObservatoryService только если в конфигах есть observatory — иначе Xray не запустится
  # («not all dependencies are resolved»)
  SVC='"RoutingService"'
  grep -qs '"observatory"\|"burstObservatory"' "$CONF"/*.json && SVC='"RoutingService", "ObservatoryService"'
  cat > "$CONF/07_api.json" <<JSON
{
  "api": {
    "tag": "api",
    "listen": "127.0.0.1:10085",
    "services": [$SVC]
  }
}
JSON
  echo "Создан $CONF/07_api.json (Xray API на 127.0.0.1:10085)"
  echo "$CONF/07_api.json" >> /opt/share/xray-selector/created.list
  NEED_RESTART=1
else
  echo "Секция api уже есть в конфигах — проверьте, что в services есть RoutingService и ObservatoryService, а listen = 127.0.0.1:10085"
fi

# починка: службы API без нужных секций не дают Xray запуститься
if ! grep -qs '"observatory"\|"burstObservatory"' "$CONF"/*.json && grep -qs '"ObservatoryService"' "$CONF"/*.json; then
  sed -i 's/, *"ObservatoryService"//; s/"ObservatoryService", *//' "$CONF"/*.json
  echo "Убрана ObservatoryService (в конфигах нет observatory)"; NEED_RESTART=1
fi
if grep -qs '"StatsService"\|statsOutbound' "$CONF"/*.json && ! grep -qs '"stats"' "$CONF"/*.json; then
  echo '{ "stats": {} }' > "$CONF/08_stats.json"
  echo "Добавлен $CONF/08_stats.json (секция stats)"; NEED_RESTART=1
fi

if [ "$NEED_RESTART" = 1 ]; then
  echo "Перезапускаю XKeen…"
  xkeen -restart || echo "Не удалось перезапустить — выполните: xkeen -restart"
fi

/opt/etc/init.d/S99xray-selector start
sleep 1
echo
echo "Готово. Панель: http://$LAN_IP:$PORT"
[ "$NOBAL" = 1 ] && echo "В конфигах XKeen нет группы серверов — мастер в панели предложит создать её одной кнопкой."
echo "Пароль на панель необязателен — его можно задать в Настройки → Пароль."
echo "Удалить панель: sh /opt/share/xray-selector/install.sh --uninstall"
echo "Журнал: /opt/var/log/xray-selector.log"
