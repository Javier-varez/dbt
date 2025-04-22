{
  description = "Daedalean build tool";

  inputs.nixpkgs.url = "nixpkgs/nixos-24.11";

  outputs =
    { self, nixpkgs }:
    let
      supportedSystems = [
        "x86_64-linux"
        "x86_64-darwin"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
    in
    {

      # Provide some binary packages for selected system types.
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
          dbtApp = pkgs.buildGoModule rec {
            pname = "dbt-app";
            version = "v3.1.0-dev";
            src = ./.;
            vendorHash = "sha256-y0AHBjCnZv2c7r/NXFrJd2dtkX1fMQzRbOmqnw7J4DM=";
            tags = [
              "semver-override=${version}"
            ];
          };
        in
        {
          dbt = pkgs.buildFHSUserEnv {
            name = "dbt";
            targetPkgs = pkgs: [
              dbtApp

              # dbt dependencies
              pkgs.bash
              pkgs.ninja
              pkgs.git
              pkgs.go
            ];
            runScript = "dbt";
          };
        }
      );

      # Add dependencies that are only needed for development
      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [
              go
              gopls
              gotools
              go-tools
            ];
          };
        }
      );

      # The default package for 'nix build'. This makes sense if the
      # flake provides only one package or there is a clear "main"
      # package.
      defaultPackage = forAllSystems (system: self.packages.${system}.dbt);
    };
}
