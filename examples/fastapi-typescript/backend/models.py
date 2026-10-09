from pydantic import BaseModel as Model

class Identity(Model):
    id: int

class Profile(Model):
    email: str

class User(Identity):
    profile: Profile
    display_name: str

class Unused(Model):
    obsolete: str
