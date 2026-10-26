sudo k3s-killall.sh
sudo k3s-uninstall.sh
rm -Rvf ~/.ops ~/.local/bin/ops
sudo userdel trustant
sudo groupdel trustant
sudo rm -Rvf /home/trustant


