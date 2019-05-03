job "sleeper" {
  datacenters = ["dc1"]

  group "sleeper" {

    ephemeral_disk {
      size = 150
    }

    task "sleeper" {
      driver  = "raw_exec"
      config {
        command = "sleep"
        args    = ["9000"]
      }

      resources {
        cpu    = 100
        memory = 100
      }
    }
  }
}
