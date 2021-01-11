locals { timestamp = regex_replace(timestamp(), "[- TZ:]", "") }

source "amazon-ebs" "latest_windows_2019" {
  ami_name       = "nomad-e2e-windows-2019-amd64-${local.timestamp}"
  communicator   = "winrm"
  instance_type  = "t2.medium"
  region         = "us-east-1"
  user_data_file = "windows-shared/setupwinrm.ps1"
  winrm_insecure = true
  winrm_use_ssl  = true
  winrm_username = "Administrator"

  source_ami_filter {
    filters = {
      name                = "Windows_Server-2019-English-Full-ContainersLatest-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    most_recent = true
    owners      = ["amazon"]
  }

  tags = {
    OS = "Windows2019"
  }
}

build {
  sources = ["source.amazon-ebs.latest_windows_2019"]

  provisioner "powershell" {
    elevated_password = build.Password
    elevated_user     = "Administrator"

    scripts = [
      "windows-shared/disable-windows-updates.ps1",
      "windows-shared/fix-tls.ps1",
      "windows-shared/install-nuget.ps1",
      "windows-shared/install-tools.ps1",
      "windows-2019-amd64/install-docker.ps1",
      "windows-shared/setup-directories.ps1",
      "windows-shared/install-openssh.ps1",
      "windows-shared/install-consul.ps1"
    ]
  }

  provisioner "file" {
    destination = "/opt"
    source      = "../config"
  }

  provisioner "file" {
    destination = "/opt/provision.ps1"
    source      = "./windows-shared/provision.ps1"
  }

  provisioner "powershell" {
    elevated_password = build.Password
    elevated_user     = "Administrator"
    inline            = ["/opt/provision.ps1 -nomad_version 0.12.7 -nostart"]
  }

  provisioner "powershell" {
    inline = [
      "C:\\ProgramData\\Amazon\\EC2-Windows\\Launch\\Scripts\\SendWindowsIsReady.ps1 -Schedule",
      "C:\\ProgramData\\Amazon\\EC2-Windows\\Launch\\Scripts\\InitializeInstance.ps1 -Schedule",
      "C:\\ProgramData\\Amazon\\EC2-Windows\\Launch\\Scripts\\SysprepInstance.ps1 -NoShutdown"
    ]
  }
}
