---
myst:
  html_meta:
    "description lang=en": "Use authd with a Samba server."
---

# Using authd with Samba

The user identifiers (UIDs) and group identifiers (GIDs) assigned by authd are
unique to each machine. This means that when using authd with Samba, the UIDs
and GIDs of users and groups on the Samba server will not match those on the
client machines, which leads to permission issues.

To avoid these issues, you can use Samba with ID mapping. This ensures that the
UIDs and GIDs are mapped correctly across all machines.

## Setting up Samba with ID mapping

This guide will walk you through setting up a Samba server with ID mapping. By
following the steps outlined below, a user `alice` will be able to access a
shared directory on the server from a client machine.

---

### Steps for the server

1. **Install Samba:**
   Install the Samba server package:

   ```shell
   sudo apt update
   sudo apt install samba
   ```

2. **Create the shared directory:**
   Create the directory to be shared and set ownership to the `alice` user:

   ```shell
   sudo mkdir -p /srv/samba/alice
   sudo chown alice:alice /srv/samba/alice
   ```

3. **Edit Samba configuration:**
   Open the Samba configuration file:

   ```shell
   sudo editor /etc/samba/smb.conf
   ```

   Add the following section at the end of the file:

   ```ini
   [alice]
   path = /srv/samba/alice
   browsable = yes
   writable = yes
   valid users = alice
   ```

   ```{admonition} Explanation
   :class: information
   This section defines a Samba share named `alice` located at
   `/srv/samba/alice`. It is visible to users on the network (`browsable`),
   allows writing (`writable`), and restricts access to the `alice` user (`valid
   users`).
   ```

4. **Create a Samba user for `alice`:**
   Add the `alice` user to the Samba database and set a password:

   ```shell
   sudo smbpasswd -a alice
   ```

   Follow the prompts to set the Samba password for the user.

5. **Restart Samba service:**
   Restart the Samba service to apply the changes:

   ```shell
   sudo systemctl restart smbd
   ```

---

### Steps for the client

1. **Install Samba client:**
   Install the required packages for connecting to Samba shares:

   ```shell
   sudo apt update
   sudo apt install smbclient cifs-utils
   ```

2. **Test access to the share:**
   Test connectivity using `smbclient`, making sure to replace `$SERVER` with
   the Samba server's hostname or IP address:

   ```shell
   smbclient //$SERVER/alice -U alice
   ```

   Enter the Samba password for `alice` when prompted. If successful, a `smb: \>`
   prompt appears.

3. **Mount the share:**
   Create a mount point for the share:

   ```shell
   mkdir -p /home/alice/samba
   ```

   Mount the share using the `cifs` filesystem type:

   ```shell
   sudo mount -t cifs //$SERVER/alice /home/alice/samba -o user=alice,uid=$(id -u alice),gid=$(id -g alice)
   ```

   Enter the Samba password for `alice` when prompted.

4. **Optional: Add the share to `/etc/fstab` for persistent mounting:**
   To automatically mount the share at boot, use a credentials file:

   - Create a credentials file:

     ```shell
     sudo editor /etc/samba/credentials
     ```

     Add the following lines:

     ```
     username=alice
     password=YOUR_PASSWORD
     ```

   - Secure the credentials file:

     ```shell
     sudo chmod 600 /etc/samba/credentials
     ```

   - Update `/etc/fstab`:

     ```
     //$SERVER/alice /home/alice/samba cifs credentials=/etc/samba/credentials,uid=alice,gid=alice 0 0
     ```

5. **Verify the mount:**
   As the user `alice`, try accessing the shared directory:

   ```shell
   ls -la /home/alice/samba
   ```

   Verify write access by creating a test file:

   ```shell
   touch /home/alice/samba/test
   ```

6. **Test enforced access control (optional):**

   ```{admonition} Security note
   :class: note
   **Security Note:** Files and directories in the share may appear as owned by
   `alice` on the client, but access control is enforced by the server.

   For example, if `alice` does not have permission on the server, access will
   be denied even if ownership appears correct on the client.
   ```

   To test this, you can create a restricted directory on the server and attempt
   to access it on the client:

   - Create a restricted directory on the server:

     ```shell
     sudo mkdir /srv/samba/alice/secrets
     sudo chmod 700 /srv/samba/alice/secrets
     ```

   - Attempt to access it on the client:

     ```shell
     ls /home/alice/samba/secrets
     ```

     The terminal output will indicate that the user does not have access to the
     restricted directory:

     ```
     ls: cannot open directory '/home/alice/samba/secrets': Permission denied
     ```

---

### Cleanup

#### On the server

1. **Delete the shared directory:**
   Remove the directory used for the Samba share:

   ```shell
   sudo rm -rf /srv/samba/alice
   ```

2. **Purge installed Samba packages:**
   If Samba is no longer needed, uninstall it completely:

   ```shell
   sudo apt purge samba samba-common
   sudo apt autoremove
   ```

---

#### On the client

1. **Unmount the shared directory:**

   ```shell
   sudo umount /home/alice/samba
   ```

2. **Delete the mount point:**

   ```shell
   rmdir /home/alice/samba
   ```

3. **Remove fstab entry:**
   If you added the share to `/etc/fstab`, remove its entry:

   ```shell
   sudo editor /etc/fstab
   ```

   Locate and delete the line referencing the Samba share, then save and close.

4. **Delete credentials file:**
   If a credentials file was used, remove it:

   ```shell
   sudo rm /etc/samba/credentials
   ```

5. **Purge installed Samba client packages:**
   If Samba client tools are no longer needed, uninstall them:

   ```shell
   sudo apt purge samba-common smbclient cifs-utils
   sudo apt autoremove
   ```

---

## Additional resources

For a complete guide to setting up Samba on your client and server, see [Samba](https://documentation.ubuntu.com/server/how-to/samba/) in the Ubuntu Server documentation.

