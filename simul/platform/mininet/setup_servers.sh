#!/usr/bin/env bash
cd "$(dirname ${BASH_SOURCE[0]})"

SERVER_GW="$1"
SERVERS="$@"
KEYS=/tmp/server_keys

rm $KEYS
for s in $SERVERS; do
	echo Starting to install on $s
	login=root@$s
	ip=$( host $s | sed -e "s/.* //" )
	ssh-keygen -R $s
	ssh-keygen -R $ip
	ssh-keyscan -t ssh-ed25519 $s >> ~/.ssh/known_hosts
	ssh-copy-id $login
	ssh $login "echo -e '\n\n\n' | ssh-keygen"
	ssh $login cat .ssh/id_rsa.pub >> $KEYS
	if ! ssh $login "grep -q 14.04 /etc/issue"; then
		clear
		echo "$s does not have Ubuntu 14.04 installed - aborting"
		exit 1
	fi
	scp install_mininet.sh $login:
	ssh $login "./install_mininet.sh &> /dev/null"
done

echo -en "\n\nPlease press <enter> to start waiting on installation"
read

DONE=0
echo $SERVERS
NBR=$( echo $SERVERS | wc -w )
while [ $DONE -lt $NBR ]; do
	DONE=0
	clear
	echo "$( date ) - Waiting on $NBR servers - $DONE are done"
	sleep 2
	for s in $SERVERS; do
		if ! ssh root@$s "ps ax | grep -v ps | grep install | grep -q mininet"; then
			DONE=$(( DONE + 1 ))
		fi
	done
done

clear
echo "All servers are done installing - copying ssh-keys"

for s in $SERVERS; do
	login=root@$s
	cat $KEYS | ssh $login "cat - >> .ssh/authorized_keys"
	ip=$( host $s | sed -e "s/.* //" )
	ssh root@$SERVER_GW "ssh-keyscan -t ssh-ed25519 $s >> .ssh/known_hosts"
	ssh root@$SERVER_GW "ssh-keyscan -t ssh-ed25519 $ip >> .ssh/known_hosts"
done
