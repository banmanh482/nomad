job "rebooter" {
  datacenters = ["dc1"]
  type = "batch"

  constraint {
    attribute = "${node.unique.id}"
    operator  = "="
    # value intentionally left blank; must be templated
  }

  group "rebooter" {

    ephemeral_disk {
      size = 150
    }

    task "rebooter" {
      driver  = "raw_exec"
      config {
        command = "bash"
        args    = ["-c", "echo 'sleep 5 && reboot' > rebooter.sh; chmod +x rebooter.sh; ./rebooter.sh &"]
      }
      user    = "root"

      resources {
        cpu    = 100
        memory = 100
      }
    }
  }
}
