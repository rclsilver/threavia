{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.threavia-backend-codex;
in
{
  options.services.threavia-backend-codex = {
    enable = lib.mkEnableOption "the Threavia backend for Codex CLI";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The backend to run.";
    };

    instanceName = lib.mkOption {
      type = lib.types.str;
      default = config.networking.hostName;
      defaultText = lib.literalExpression "config.networking.hostName";
      description = ''
        How this machine names itself to Core. It is what a person picks from
        when starting a Session, so it should read like the machine. Changing
        it renames the backend at its next connection.
      '';
    };

    coreAddress = lib.mkOption {
      type = lib.types.str;
      example = "threavia.example.com:9090";
      description = "host:port of the Core control stream (gRPC).";
    };

    coreApi = lib.mkOption {
      type = lib.types.str;
      example = "https://threavia.example.com";
      description = ''
        Base URL of the Core client API. The backend calls it once, to register;
        everything after that goes over the control stream.
      '';
    };

    user = lib.mkOption {
      type = lib.types.str;
      example = "thomas";
      description = ''
        The account the agent works as. It is not a formality: this backend
        edits that account's checkouts, runs commands with its permissions, and
        reads the Codex CLI credentials in its home. On a laptop it is the
        person using the machine; the execution policy is a guard rail inside
        that account, never a sandbox around it.
      '';
    };

    stateDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/threavia-backend-codex";
      description = ''
        Where the backend keeps its identity, its event log and its skill cache.
        Losing it means registering again, and anything it had not yet delivered
        to Core is lost with it.
      '';
    };

    discoveryRoots = lib.mkOption {
      type = lib.types.listOf lib.types.path;
      default = [ ];
      example = lib.literalExpression ''[ "/home/thomas/git" ]'';
      description = ''
        Where this machine keeps the projects a Job is about: where to look for
        a project directory that has no binding here yet, and what a Job may
        read without asking beyond the directory it works in.

        The second part is why they are worth setting. A Job on one repository
        routinely reads the repository beside it, and without this every such
        read is a question put to a person who may be asleep.

        It bounds discovery, search and prompting, never access: what the agent
        may reach is the account's real permissions, and writing still follows
        the execution policy.
      '';
    };

    defaultWorkingDirectory = lib.mkOption {
      type = lib.types.path;
      description = ''
        Where a Job runs when its Session names no directory. Without one the
        agent would inherit whatever directory the service happens to start in,
        which is accidental rather than chosen.
      '';
    };

    scratchDir = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/srv/threavia-scratch";
      description = ''
        Where a Session writes the files it will read back, one directory per
        Run. Without one the agent uses /tmp and then needs an approval to read
        back what it wrote a second earlier, because anything outside the
        working directory is a prompt.

        Null leaves it to the backend, which puts it in the home of the account
        it runs as: <literal>~/.threavia/codex/scratch</literal>. These are
        that person's files, made by an agent running as them, so they belong
        where that person would look for them and where no permission has to be
        arranged. The state directory would be the wrong place twice — it mixes
        what the backend owns with what the work produced, and it asks the
        account for write access it has no other reason to want.

        Whatever the location, what nobody came back for after a week is swept
        when the backend starts.
      '';
    };

    extraPackages = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = [ ];
      example = lib.literalExpression "[ pkgs.go pkgs.nodejs pkgs.kubectl ]";
      description = ''
        More tools on the service PATH, for the languages and the tooling the
        work actually needs. The default below is a text toolbox and nothing
        more: it is what the agent reads a repository with, not what it builds
        one with.
      '';
    };

    codexPackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      example = lib.literalExpression "pkgs.codex";
      description = ''
        The Codex CLI the backend drives, added to the service PATH.
        Set this to pkgs.codex, or set codexBinary to an existing installation.
      '';
    };

    codexBinary = lib.mkOption {
      type = lib.types.str;
      default = if cfg.codexPackage != null then lib.getExe cfg.codexPackage else "codex";
      defaultText = lib.literalExpression "lib.getExe config.services.threavia-backend-codex.codexPackage";
      description = "Path of the Codex CLI executable.";
    };

    tls = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Whether the control stream uses TLS. True by default because the
          stream carries everything an agent does; turn it off only for a Core
          on the same machine.
        '';
      };

      caFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        description = "A CA to trust beyond the system store, for a private one.";
      };
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/threavia-backend";
      description = ''
        A file of <literal>NAME=value</literal> lines read at start, for what
        does not belong in the Nix store. The one-shot registration token goes
        here as <literal>THREAVIA_BACKEND_REGISTRATION_TOKEN</literal>; it is
        needed only until the backend has registered, after which its identity
        lives in the state directory.
      '';
    };

    settings = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      example = lib.literalExpression ''{ THREAVIA_BACKEND_MAX_CONCURRENT_RUNS = "2"; }'';
      description = ''
        Extra environment for the backend, for the settings this module does not
        name. They win over the ones it does.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.codexPackage != null || cfg.codexBinary != "codex";
        message = ''
          services.threavia-backend-codex: no Codex CLI to drive. Set
          codexPackage, or codexBinary to an absolute path: the service runs
          with a PATH of its own and will not find one on the person's.
        '';
      }
    ];

    # StateDirectory makes the default path and nothing else; a path chosen
    # elsewhere has to be made, or the first start fails on a missing database.
    # Private to the account, like the default: it holds the backend credential.
    systemd.tmpfiles.rules = lib.optional (
      cfg.stateDir != "/var/lib/threavia-backend-codex"
    ) "d ${cfg.stateDir} 0700 ${cfg.user} - -";

    systemd.services.threavia-backend-codex = {
      description = "Threavia backend for Codex CLI";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      environment = {
        THREAVIA_BACKEND_INSTANCE_NAME = cfg.instanceName;
        THREAVIA_BACKEND_CORE_ADDRESS = cfg.coreAddress;
        THREAVIA_BACKEND_CORE_API = cfg.coreApi;
        THREAVIA_BACKEND_TLS_ENABLED = lib.boolToString cfg.tls.enable;
        THREAVIA_BACKEND_STATE_PATH = "${cfg.stateDir}/state.db";
        THREAVIA_BACKEND_SKILL_CACHE_PATH = "${cfg.stateDir}/skills";
        THREAVIA_BACKEND_CODEX_BINARY = cfg.codexBinary;
        THREAVIA_BACKEND_DEFAULT_WORKING_DIRECTORY = toString cfg.defaultWorkingDirectory;
      }
      // lib.optionalAttrs (cfg.discoveryRoots != [ ]) {
        THREAVIA_BACKEND_DISCOVERY_ROOTS = lib.concatMapStringsSep "," toString cfg.discoveryRoots;
      }
      // lib.optionalAttrs (cfg.tls.caFile != null) {
        THREAVIA_BACKEND_TLS_CA_FILE = toString cfg.tls.caFile;
      }
      // lib.optionalAttrs (cfg.scratchDir != null) {
        THREAVIA_BACKEND_SCRATCH_PATH = toString cfg.scratchDir;
      }
      // cfg.settings;

      # Everything the agent runs it runs through this PATH, so a bare `git` in
      # a command has to resolve. It is deliberately short — the agent is
      # working in someone's checkout, not in a general-purpose shell — but it
      # is no longer minimal, and that was a mistake worth naming.
      #
      # With only git, ssh, coreutils and bash here, the agent could not run
      # `sed`, `rg` or `jq`, so it prefixed every single command with
      # `export PATH=/run/current-system/sw/bin:$PATH && …` to reach them. That
      # is a command writing PATH, which Codex CLI prompts about whatever else
      # the command does — so the shortest possible PATH turned every read into
      # a permission request. A toolbox nobody has to work around is the one
      # that gets used as given.
      path = [
        pkgs.git
        pkgs.openssh
        pkgs.coreutils
        pkgs.bash
        pkgs.gnugrep
        pkgs.gnused
        pkgs.gawk
        pkgs.findutils
        pkgs.diffutils
        pkgs.ripgrep
        pkgs.jq
        pkgs.gnutar
        pkgs.gzip
        pkgs.less
        pkgs.which
      ]
      ++ cfg.extraPackages
      ++ lib.optional (cfg.codexPackage != null) cfg.codexPackage;

      serviceConfig = {
        ExecStart = lib.getExe cfg.package;
        User = cfg.user;
        WorkingDirectory = cfg.stateDir;
        StateDirectory = lib.mkIf (
          cfg.stateDir == "/var/lib/threavia-backend-codex"
        ) "threavia-backend-codex";
        # systemd makes it 0755 otherwise, and it holds the backend credential:
        # whoever reads that can act as this backend towards Core.
        StateDirectoryMode = "0700";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;

        # A backend that cannot reach Core keeps its queue and waits, so a
        # restart is the right answer to every exit.
        Restart = "always";
        RestartSec = "5s";

        # Hardening stops here on purpose. The agent's whole job is to read and
        # write the account's files and run its tools, so ProtectHome,
        # ReadOnlyPaths and a private namespace would break the thing being
        # asked for. What bounds it is the execution policy and the account's
        # own permissions.
        NoNewPrivileges = true;
      };
    };
  };
}
