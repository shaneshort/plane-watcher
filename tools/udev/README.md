# Udev Rules

Host-side helper rules for development tools that need direct hardware access.

## Siglent SDS814X-HD USBTMC

The `99-siglent-sds800x-hd-usbtmc.rules` rule grants local interactive access
to the Siglent SDS814X-HD scope when connected over the USB device port.

It matches the USBTMC character device by USB vendor/product ID:

- vendor: `f4ec`
- product: `1017`

Those IDs correspond to the bench scope as reported by `lsusb -v`:

- manufacturer string: `Siglent`
- product string: `SDS814XHD`

Install on the Linux workstation:

```sh
sudo cp tools/udev/99-siglent-sds800x-hd-usbtmc.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules
sudo udevadm trigger
```

Then unplug/replug the scope or verify the node directly:

```sh
ls -l /dev/usbtmc*
```

The rule uses both:

- `GROUP="plugdev"` for shared bench-machine access
- `TAG+="uaccess"` for active desktop-session access

If the local user is not already in `plugdev`, add them and re-login:

```sh
sudo usermod -aG plugdev "$USER"
```
