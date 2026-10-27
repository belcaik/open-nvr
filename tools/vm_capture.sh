#!/usr/bin/env bash
# Captura el tráfico de la tarjeta de red de una VM de libvirt sin root, con el
# objeto filter-dump de QEMU. Se añade y se quita en caliente, sin reiniciar la
# VM. Requiere pertenecer al grupo libvirt.
#
#   tools/vm_capture.sh start [dominio] [nombre]   # por defecto: win10, fecha-hora
#   tools/vm_capture.sh stop  [dominio]
#   tools/vm_capture.sh status [dominio]
#
# El pcap lo escribe el proceso QEMU (usuario libvirt-qemu), que no puede
# escribir en $HOME, así que se guarda en /tmp/opennvr-vmcap/. Al parar, el
# archivo se mueve a captures/vm/ del repositorio.
set -euo pipefail

ACTION=${1:-status}
DOMAIN=${2:-win10}
NAME=${3:-$(date +%Y%m%d-%H%M%S)}
CONN=qemu:///system
FILTER_ID=opennvrcap
SPOOL=/tmp/opennvr-vmcap
DEST=$(cd "$(dirname "$0")/.." && pwd)/captures/vm

hmp() { virsh -c "$CONN" qemu-monitor-command "$DOMAIN" --hmp "$1"; }

netdev() {
    hmp 'info network' | sed -n 's/^ *\\ *\(hostnet[0-9]*\):.*/\1/p' | head -1
}

case "$ACTION" in
start)
    mkdir -p "$SPOOL" && chmod 1777 "$SPOOL"
    nd=$(netdev)
    [ -n "$nd" ] || { echo "no se encontró el netdev de $DOMAIN" >&2; exit 1; }
    file="$SPOOL/$DOMAIN-$NAME.pcap"
    out=$(hmp "object_add filter-dump,id=$FILTER_ID,netdev=$nd,queue=all,file=$file,maxlen=65535")
    if [ -n "$out" ]; then
        echo "$out" >&2
        if [[ $out == *Vhost* ]]; then
            iface=$(virsh -c "$CONN" domiflist "$DOMAIN" | awk 'NR>2 && $1 {print $1; exit}')
            cat >&2 <<EOF

La interfaz usa vhost-net, incompatible con filter-dump. Opciones:
  a) Capturar en el host (requiere sudo, no toca la VM):
       sudo tcpdump -i $iface -s 0 -U -w $DEST/$DOMAIN-$NAME.pcap
  b) Desactivar vhost: 'virsh edit $DOMAIN', añadir <driver name='qemu'/>
     dentro de <interface>, apagar y arrancar la VM, y repetir este comando.
EOF
        fi
        exit 1
    fi
    echo "$file" > "$SPOOL/.current-$DOMAIN"
    echo "capturando $DOMAIN ($nd) en $file"
    ;;
stop)
    hmp "object_del $FILTER_ID"
    file=$(cat "$SPOOL/.current-$DOMAIN")
    mkdir -p "$DEST"
    cp "$file" "$DEST/" && rm -f "$SPOOL/.current-$DOMAIN"
    echo "captura guardada en $DEST/$(basename "$file")"
    ;;
status)
    if [ -f "$SPOOL/.current-$DOMAIN" ]; then
        file=$(cat "$SPOOL/.current-$DOMAIN")
        echo "capturando: $file ($(stat -c %s "$file") bytes)"
    else
        echo "sin captura activa para $DOMAIN"
    fi
    ;;
*)
    echo "uso: $0 start|stop|status [dominio] [nombre]" >&2
    exit 2
    ;;
esac
