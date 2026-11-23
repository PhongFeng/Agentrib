"""Workspace-scoped modeling tools backed by the upstream MCP for Blender server."""
import argparse
import asyncio
import os
import re
import shutil
import struct
import sys
import tempfile
from pathlib import Path
from typing import Annotated, Literal

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.server.fastmcp import FastMCP
from pydantic import BaseModel, Field, FiniteFloat, model_validator


Vector = Annotated[list[FiniteFloat], Field(min_length=3, max_length=3)]
Channel = Annotated[float, Field(ge=0, le=1)]


class Part(BaseModel, extra="forbid"):
    name: str = Field(min_length=1, max_length=64)
    shape: Literal["box", "sphere", "cylinder", "cone", "mesh"]
    location: Vector = [0, 0, 0]
    scale: Vector = [1, 1, 1]
    rotation_degrees: Vector = [0, 0, 0]
    color: Annotated[list[Channel], Field(min_length=3, max_length=4, description="RGB or RGBA channels from 0 to 1")] = [0.6, 0.6, 0.6]
    vertices: list[Vector] = Field(default_factory=list, max_length=8192)
    faces: list[Annotated[list[int], Field(min_length=3, max_length=128)]] = Field(default_factory=list, max_length=8192)

    @model_validator(mode="after")
    def validate_mesh(self):
        if self.shape == "mesh":
            if not self.faces or any(i < 0 or i >= len(self.vertices) for face in self.faces for i in face):
                raise ValueError("Mesh faces must reference existing vertices")
        elif self.faces or self.vertices:
            raise ValueError("Only the mesh shape takes vertices and faces")
        return self


async def blender_call(code: str):
    # Use the paired server installed in this adapter's virtual environment.
    executable = shutil.which("mcp-for-blender", path=str(Path(sys.executable).parent))
    if not executable:
        raise RuntimeError("mcp-for-blender is missing from the adapter environment")
    upstream = StdioServerParameters(command=executable, cwd=str(Path(__file__).resolve().parent),
        env={"BLENDER_HOST": "127.0.0.1", "BLENDER_PORT": os.environ.get("BLENDER_PORT", "9876"),
             "DISABLE_TELEMETRY": "1"})
    async with stdio_client(upstream) as pipes, ClientSession(*pipes) as session:
        await session.initialize()
        result = await session.call_tool("execute_blender_code", {"code": code})
        reply = "\n".join(item.text for item in result.content if item.type == "text")
        if result.isError or not reply.rstrip().endswith("AGENTRIB_OK"):
            raise RuntimeError("Blender operation failed: " + reply[:1200])


# Only this fixed script runs in Blender. All agent inputs are validated data.
EXPORT = """
import bpy, bmesh, math
collection = bpy.data.collections.new("Agentrib Export")
bpy.context.scene.collection.children.link(collection)
meshes, materials = [], []
try:
    for part in parts:
        mesh = bpy.data.meshes.new(part["name"])
        meshes.append(mesh)
        kind = part["shape"]
        if kind == "mesh":
            mesh.from_pydata(part["vertices"], [], part["faces"])
            mesh.update()
        else:
            bm = bmesh.new()
            try:
                if kind == "box":
                    bmesh.ops.create_cube(bm, size=1)
                elif kind == "sphere":
                    bmesh.ops.create_uvsphere(bm, u_segments=24, v_segments=12, radius=0.5)
                else:
                    bmesh.ops.create_cone(bm, cap_ends=True, segments=24, radius1=0.5,
                                          radius2=0 if kind == "cone" else 0.5, depth=1)
                bm.to_mesh(mesh)
            finally:
                bm.free()
        obj = bpy.data.objects.new(part["name"], mesh)
        collection.objects.link(obj)
        obj.location, obj.scale = part["location"], part["scale"]
        obj.rotation_euler = [math.radians(angle) for angle in part["rotation_degrees"]]
        mat = bpy.data.materials.new(part["name"])
        materials.append(mat)
        color = part["color"]
        mat.diffuse_color = color if len(color) == 4 else [*color, 1]
        mesh.materials.append(mat)
    bpy.context.view_layer.update()
    status = bpy.ops.export_scene.gltf(filepath=target, export_format="GLB", collection=collection.name)
    if "FINISHED" not in status:
        raise RuntimeError("glTF export failed")
finally:
    for obj in list(collection.objects):
        bpy.data.objects.remove(obj, do_unlink=True)
    bpy.data.collections.remove(collection)
    for mesh in meshes:
        bpy.data.meshes.remove(mesh)
    for mat in materials:
        bpy.data.materials.remove(mat)
print("AGENTRIB_OK")
"""


def model_server(workspace: Path):
    workspace = workspace.resolve(strict=True)
    server = FastMCP("Agentrib Blender")

    @server.tool()
    async def create_model(filename: str, parts: Annotated[list[Part], Field(min_length=1, max_length=256)]) -> str:
        """Create or replace assets/models/<filename>.glb. Supply the filename including .glb.
        Each part takes a name and shape (box, sphere, cylinder, cone or mesh). Use numeric XYZ
        vectors for location, scale and rotation_degrees, and numeric RGB or RGBA colors (0 to 1).
        Mesh faces index into a list of XYZ vertices. Coordinates are Blender Z-up.
        Transforms are per part. The exported GLB uses glTF coordinates and embeds the materials.
        """
        if not re.fullmatch(r"[\w-][\w.-]{0,95}\.glb", filename):
            raise ValueError("Use a plain .glb filename")
        directory = workspace
        for folder in ("assets", "models"):
            directory = directory / folder
            if directory.is_symlink() or not directory.resolve().is_relative_to(workspace):
                raise ValueError("Model directory must be inside the current worktree")
            directory.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=directory, prefix=".agentrib-") as scratch:
            output = Path(scratch) / "model.glb"
            variables = f"parts = {[part.model_dump() for part in parts]!r}\ntarget = {str(output)!r}\n"
            await asyncio.wait_for(blender_call(variables + EXPORT), timeout=90)
            with output.open("rb") as file:
                magic, version, size = struct.unpack("<4sII", file.read(12))
            if magic != b"glTF" or version != 2 or size != output.stat().st_size:
                raise RuntimeError("Blender returned an invalid GLB")
            output.replace(directory / filename)
        return f"assets/models/{filename} ({size} bytes)"

    return server


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--workspace", type=Path)
    mode.add_argument("--check", action="store_true")
    args = parser.parse_args()
    if args.check:
        asyncio.run(asyncio.wait_for(blender_call("import bpy\nprint(bpy.app.version_string)\nprint('AGENTRIB_OK')"), timeout=12))
    else:
        model_server(args.workspace).run()
